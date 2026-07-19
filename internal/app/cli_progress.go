package app

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"forge_worker/internal/pipeline"
	"forge_worker/internal/state"
	"forge_worker/internal/task"

	"github.com/mattn/go-isatty"
	"github.com/schollz/progressbar/v3"
)

const localTaskPollInterval = 500 * time.Millisecond
const localProgressReportBucket = 5
const taskPerformanceReportInterval = 10 * time.Second
const downloadPerformanceReportInterval = 2 * time.Second

type taskSnapshot struct {
	Task     state.TaskRecord
	Steps    []state.StepRecord
	Commands []state.StepCommandRecord
}

type taskObserver struct {
	database *state.DB
	taskUUID string
	output   io.Writer
	bar      *progressbar.ProgressBar

	last        taskSnapshot
	haveLast    bool
	lastReadErr string
	stepReports map[string]time.Time
}

func startTaskObserver(parent context.Context, database *state.DB, taskUUID string, output io.Writer) func() {
	ctx, cancel := context.WithCancel(parent)
	observer := newTaskObserver(database, taskUUID, output)
	done := make(chan struct{})
	go func() {
		defer close(done)
		observer.refresh(ctx)
		ticker := time.NewTicker(localTaskPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				observer.refresh(ctx)
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
			finalCtx, finalCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer finalCancel()
			observer.refresh(finalCtx)
			observer.finish()
		})
	}
}

func newTaskObserver(database *state.DB, taskUUID string, output io.Writer) *taskObserver {
	observer := &taskObserver{database: database, taskUUID: taskUUID, output: output, stepReports: make(map[string]time.Time)}
	if isTerminalWriter(output) {
		observer.bar = progressbar.NewOptions(100,
			progressbar.OptionSetWriter(output),
			progressbar.OptionSetWidth(28),
			progressbar.OptionShowCount(),
			progressbar.OptionShowElapsedTimeOnFinish(),
			progressbar.OptionSetDescription("forge-worker"),
			progressbar.OptionThrottle(100*time.Millisecond),
			progressbar.OptionClearOnFinish(),
		)
		_ = observer.bar.RenderBlank()
	}
	return observer
}

func (o *taskObserver) refresh(ctx context.Context) {
	snapshot, err := o.snapshot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		message := err.Error()
		if message != o.lastReadErr {
			o.println("status read failed: " + message)
			o.lastReadErr = message
		}
		return
	}
	o.lastReadErr = ""
	if o.bar != nil {
		o.bar.Describe(taskProgressDescription(snapshot))
		_ = o.bar.Set(percentInt(snapshot.Task.Progress))
	}
	for _, line := range o.changedLines(snapshot) {
		o.println(line)
	}
	o.last = snapshot
	o.haveLast = true
}

func (o *taskObserver) snapshot(ctx context.Context) (taskSnapshot, error) {
	record, err := o.database.GetTask(ctx, o.taskUUID)
	if err != nil {
		return taskSnapshot{}, err
	}
	steps, err := o.database.ListSteps(ctx, o.taskUUID)
	if err != nil {
		return taskSnapshot{}, err
	}
	commands, err := o.database.ListStepCommands(ctx, o.taskUUID)
	if err != nil {
		return taskSnapshot{}, err
	}
	return taskSnapshot{Task: record, Steps: steps, Commands: commands}, nil
}

func (o *taskObserver) changedLines(snapshot taskSnapshot) []string {
	var lines []string
	if o.shouldReportTask(snapshot.Task) {
		lines = append(lines, taskStatusLine(snapshot))
	}

	lastSteps := make(map[string]state.StepRecord, len(o.last.Steps))
	if o.haveLast {
		for _, step := range o.last.Steps {
			lastSteps[step.Name] = step
		}
	}
	for _, step := range snapshot.Steps {
		last, hadLast := lastSteps[step.Name]
		if o.shouldReportStep(step, last, hadLast) {
			lines = append(lines, stepStatusLine(step))
			o.stepReports[step.Name] = time.Now()
		}
	}

	start := 0
	if o.haveLast {
		start = len(o.last.Commands)
	}
	if start > len(snapshot.Commands) {
		start = 0
	}
	for _, command := range snapshot.Commands[start:] {
		if command.Summary == "" {
			continue
		}
		lines = append(lines, localCommandLine(command))
	}
	return lines
}

func (o *taskObserver) shouldReportTask(record state.TaskRecord) bool {
	if !o.haveLast {
		return true
	}
	if record.State != o.last.Task.State {
		return true
	}
	if record.State.Terminal() {
		return true
	}
	return progressReportBucket(record.Progress) != progressReportBucket(o.last.Task.Progress)
}

func (o *taskObserver) shouldReportStep(current, last state.StepRecord, hadLast bool) bool {
	if !hadLast {
		return stepIsActiveOrDone(current)
	}
	if current.State != last.State || current.Attempt != last.Attempt {
		return true
	}
	if current.State == string(task.StepRunning) {
		if progressReportBucket(current.Progress) != progressReportBucket(last.Progress) {
			return true
		}
		lastReport := o.stepReports[current.Name]
		if hasStepTransfer(current) {
			return time.Since(lastReport) >= downloadPerformanceReportInterval
		}
		return hasStepPerformance(current) && time.Since(lastReport) >= taskPerformanceReportInterval
	}
	return false
}

func stepIsActiveOrDone(step state.StepRecord) bool {
	switch step.State {
	case string(task.StepPending):
		return false
	default:
		return true
	}
}

func stepStatusLine(step state.StepRecord) string {
	attempt := ""
	if step.MaxAttempts > 0 {
		attempt = fmt.Sprintf(" | attempt %d/%d", step.Attempt, step.MaxAttempts)
	}
	performance := formatStepPerformance(step, false)
	return fmt.Sprintf("step %s | %s | %s%s%s", pipeline.ExternalStepName(step.Name), stateDisplayName(step.State), formatPercent(step.Progress), performance, attempt)
}

func (o *taskObserver) finish() {
	if o.bar != nil {
		_ = o.bar.Finish()
	}
}

func (o *taskObserver) println(line string) {
	if o.bar != nil {
		_, _ = progressbar.Bprintln(o.bar, line)
		return
	}
	_, _ = fmt.Fprintln(o.output, line)
}

func taskProgressDescription(snapshot taskSnapshot) string {
	description := fmt.Sprintf("%s total %s", stateDisplayName(string(snapshot.Task.State)), formatPercent(snapshot.Task.Progress))
	if step, ok := currentDisplayStep(snapshot.Steps); ok {
		if hasStepTransfer(step) {
			description += " download " + formatPercent(step.Progress)
		}
		description += formatStepPerformance(step, true)
	}
	return description
}

func hasStepPerformance(step state.StepRecord) bool {
	return hasStepTransfer(step) || step.FPS > 0 || step.Speed > 0
}

func formatStepPerformance(step state.StepRecord, compact bool) string {
	separator := " | "
	if compact {
		separator = " "
	}
	result := ""
	if hasStepTransfer(step) {
		if step.TotalBytes > 0 {
			result += separator + formatBytes(step.TransferredBytes) + "/" + formatBytes(step.TotalBytes)
		} else if step.TransferredBytes > 0 {
			result += separator + formatBytes(step.TransferredBytes)
		}
		if step.BytesPerSecond > 0 {
			result += separator + formatBytes(int64(step.BytesPerSecond)) + "/s"
		}
		if step.ETASeconds > 0 {
			result += separator + "eta " + formatETA(step.ETASeconds)
		}
		return result
	}
	if step.FPS > 0 {
		result += separator + fmt.Sprintf("%.1f fps", step.FPS)
	}
	if step.Speed > 0 {
		result += separator + fmt.Sprintf("%.2fx", step.Speed)
	}
	return result
}

func hasStepTransfer(step state.StepRecord) bool {
	return step.TransferredBytes > 0 || step.TotalBytes > 0 || step.BytesPerSecond > 0 || step.ETASeconds > 0
}

func formatBytes(value int64) string {
	if value < 0 {
		value = 0
	}
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d%s", value, units[unit])
	}
	return fmt.Sprintf("%.1f%s", size, units[unit])
}

func formatETA(seconds float64) string {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return "0s"
	}
	return (time.Duration(math.Ceil(seconds)) * time.Second).String()
}

func taskStatusLine(snapshot taskSnapshot) string {
	line := fmt.Sprintf("task %s | %s | total %s", snapshot.Task.TaskUUID, stateDisplayName(string(snapshot.Task.State)), formatPercent(snapshot.Task.Progress))
	if step, ok := currentDisplayStep(snapshot.Steps); ok {
		line += " | current " + pipeline.ExternalStepName(step.Name)
	}
	return line
}

func currentDisplayStep(steps []state.StepRecord) (state.StepRecord, bool) {
	for _, step := range steps {
		switch step.State {
		case string(task.StepRunning), string(task.StepRetryWait):
			return step, true
		}
	}
	return state.StepRecord{}, false
}

func stateDisplayName(value string) string {
	return strings.ReplaceAll(value, "_", " ")
}

func formatPercent(value float64) string {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return fmt.Sprintf("%.1f%%", value)
}

func compactCommandSummary(summary string) string {
	summary = strings.Join(strings.Fields(summary), " ")
	const limit = 180
	if len(summary) <= limit {
		return summary
	}
	return summary[:limit-3] + "..."
}

func localCommandLine(command state.StepCommandRecord) string {
	return fmt.Sprintf("cmd %s | %s", pipeline.ExternalStepName(command.StepName), localCommandSummary(command.Summary))
}

func localCommandSummary(summary string) string {
	summary = strings.Join(strings.Fields(summary), " ")
	for _, marker := range []string{" | ffmpeg ", " | ffprobe ", " | packager ", " | vips "} {
		if index := strings.Index(summary, marker); index > 0 {
			return compactCommandSummary(summary[:index])
		}
	}
	return compactCommandSummary(summary)
}

func percentInt(value float64) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return int(math.Round(value))
}

func progressReportBucket(value float64) int {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return int(math.Floor(value / localProgressReportBucket))
}

func isTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}
