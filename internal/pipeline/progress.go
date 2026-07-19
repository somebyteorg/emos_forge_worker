package pipeline

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"forge_worker/internal/state"
)

const (
	commandProgressMinDelta         = 0.25
	commandProgressMinFlushInterval = 250 * time.Millisecond
)

type commandProgressKind int

const (
	commandProgressNone commandProgressKind = iota
	commandProgressStage
	commandProgressFFmpeg
	commandProgressPercent
)

type stepProgressRange struct {
	Start float64
	End   float64
}

type commandProgress struct {
	Range           stepProgressRange
	Kind            commandProgressKind
	DurationSeconds float64
}

func stageCommandProgress(start, end float64) commandProgress {
	return commandProgress{Range: newStepProgressRange(start, end), Kind: commandProgressStage}
}

func ffmpegCommandProgress(start, end, durationSeconds float64) commandProgress {
	return commandProgress{Range: newStepProgressRange(start, end), Kind: commandProgressFFmpeg, DurationSeconds: durationSeconds}
}

func percentCommandProgress(start, end float64) commandProgress {
	return commandProgress{Range: newStepProgressRange(start, end), Kind: commandProgressPercent}
}

func newStepProgressRange(start, end float64) stepProgressRange {
	start = clampProgress(start)
	end = clampProgress(end)
	if end < start {
		end = start
	}
	return stepProgressRange{Start: start, End: end}
}

func (r stepProgressRange) valid() bool {
	return r.End > r.Start
}

func (r stepProgressRange) mapFraction(fraction float64) float64 {
	if !r.valid() {
		return r.End
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	return r.Start + (r.End-r.Start)*fraction
}

func clampProgress(progress float64) float64 {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func indexedProgressRange(start, end float64, index, total int) (float64, float64) {
	progressRange := newStepProgressRange(start, end)
	if total <= 0 {
		return progressRange.Start, progressRange.End
	}
	if index < 0 {
		index = 0
	}
	if index >= total {
		index = total - 1
	}
	width := (progressRange.End - progressRange.Start) / float64(total)
	return progressRange.Start + width*float64(index), progressRange.Start + width*float64(index+1)
}

type spriteCommandProgress struct {
	start float64
	end   float64
	index int
	total int
}

func newSpriteCommandProgress(start, end float64, total int) *spriteCommandProgress {
	return &spriteCommandProgress{start: start, end: end, total: total}
}

func spriteCommandCount(groups [][]spriteSize, sheetTotal int) int {
	if sheetTotal <= 0 {
		return 0
	}
	total := 0
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		total += sheetTotal * (1 + len(group))
	}
	return total
}

func (p *spriteCommandProgress) nextStage() commandProgress {
	if p == nil || p.total <= 0 {
		return commandProgress{}
	}
	start, end := indexedProgressRange(p.start, p.end, p.index, p.total)
	p.index++
	return stageCommandProgress(start, end)
}

func (p *spriteCommandProgress) nextPercent() commandProgress {
	if p == nil || p.total <= 0 {
		return commandProgress{}
	}
	start, end := indexedProgressRange(p.start, p.end, p.index, p.total)
	p.index++
	return percentCommandProgress(start, end)
}

type commandProgressTracker struct {
	repo     *state.DB
	taskUUID string
	stepName string
	progress commandProgress

	mu        sync.Mutex
	last      float64
	lastFlush time.Time
	fps       float64
	speed     float64
	metrics   bool
}

func newCommandProgressTracker(repo *state.DB, taskUUID, stepName string, progress commandProgress) *commandProgressTracker {
	return &commandProgressTracker{repo: repo, taskUUID: taskUUID, stepName: stepName, progress: progress, last: -1}
}

func (t *commandProgressTracker) start() {
	if t == nil || !t.progress.Range.valid() {
		return
	}
	if t.progress.Kind == commandProgressFFmpeg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = t.repo.UpdateStepPerformance(ctx, t.taskUUID, t.stepName, 0, 0)
		cancel()
	}
	t.set(t.progress.Range.Start, true)
}

func (t *commandProgressTracker) finish() {
	if t == nil || !t.progress.Range.valid() {
		return
	}
	t.flushPerformance()
	t.set(t.progress.Range.End, true)
}

func (t *commandProgressTracker) onLine(stream string, line string) {
	if t == nil || !t.progress.Range.valid() {
		return
	}
	var fraction float64
	var ok bool
	switch t.progress.Kind {
	case commandProgressFFmpeg:
		if stream != "stdout" {
			return
		}
		t.captureFFmpegPerformance(line)
		fraction, ok = ffmpegProgressFraction(line, t.progress.DurationSeconds)
	case commandProgressPercent:
		fraction, ok = percentProgressFraction(line)
	default:
		return
	}
	if ok {
		t.set(t.progress.Range.mapFraction(fraction), false)
	}
}

func (t *commandProgressTracker) captureFFmpegPerformance(line string) {
	key, value, ok := splitProgressLine(line)
	if !ok {
		return
	}
	switch key {
	case "fps":
		fps, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil && !math.IsNaN(fps) && !math.IsInf(fps, 0) && fps >= 0 {
			t.mu.Lock()
			t.fps = fps
			t.metrics = true
			t.mu.Unlock()
		}
	case "speed":
		raw := strings.TrimSuffix(strings.TrimSpace(value), "x")
		speed, err := strconv.ParseFloat(raw, 64)
		if err == nil && !math.IsNaN(speed) && !math.IsInf(speed, 0) && speed >= 0 {
			t.mu.Lock()
			t.speed = speed
			t.metrics = true
			t.mu.Unlock()
		}
	case "progress":
		t.flushPerformance()
	}
}

func (t *commandProgressTracker) flushPerformance() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if !t.metrics {
		t.mu.Unlock()
		return
	}
	fps, speed := t.fps, t.speed
	t.metrics = false
	t.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = t.repo.UpdateStepPerformance(ctx, t.taskUUID, t.stepName, fps, speed)
}

func (t *commandProgressTracker) set(progress float64, force bool) {
	progress = clampProgress(progress)
	now := time.Now()

	t.mu.Lock()
	if t.last >= 0 && progress < t.last {
		t.mu.Unlock()
		return
	}
	if !force && t.last >= 0 && progress-t.last < commandProgressMinDelta && now.Sub(t.lastFlush) < commandProgressMinFlushInterval {
		t.mu.Unlock()
		return
	}
	t.last = progress
	t.lastFlush = now
	t.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = t.repo.UpdateStepProgress(ctx, t.taskUUID, t.stepName, progress)
}

func (e *Executor) setStepProgress(taskUUID, stepName string, progress float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = e.repo.UpdateStepProgress(ctx, taskUUID, stepName, progress)
}

func ffmpegProgressFraction(line string, durationSeconds float64) (float64, bool) {
	key, value, ok := splitProgressLine(line)
	if !ok {
		return 0, false
	}
	if key == "progress" && strings.TrimSpace(value) == "end" {
		return 1, true
	}
	if durationSeconds <= 0 {
		return 0, false
	}
	var secondsValue float64
	switch key {
	case "out_time_us", "out_time_ms":
		microseconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0, false
		}
		secondsValue = microseconds / 1_000_000
	case "out_time":
		parsed, ok := parseFFmpegClock(value)
		if !ok {
			return 0, false
		}
		secondsValue = parsed
	default:
		return 0, false
	}
	return secondsValue / durationSeconds, true
}

func splitProgressLine(line string) (string, string, bool) {
	left, right, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return "", "", false
	}
	left = strings.TrimSpace(left)
	if left == "" {
		return "", "", false
	}
	return left, strings.TrimSpace(right), true
}

func parseFFmpegClock(value string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 3 {
		return 0, false
	}
	hours, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, false
	}
	minutes, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, false
	}
	secondsValue, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, false
	}
	return hours*3600 + minutes*60 + secondsValue, true
}

var percentProgressPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*%`)

func percentProgressFraction(line string) (float64, bool) {
	match := percentProgressPattern.FindStringSubmatch(line)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value / 100, true
}
