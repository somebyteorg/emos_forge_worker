package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forge_worker/internal/config"
	"forge_worker/internal/pipeline"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func buildLocalRequest(cfg config.Config, taskUUID, input, output, videoProfiles, audioStrategy string, subtitles, audio, audioPackage, audioAAC, video, sprites bool, spriteSizes, spriteFrameFormat string) (task.Request, error) {
	taskUUID = strings.TrimSpace(taskUUID)
	if taskUUID == "" {
		var err error
		taskUUID, err = task.NewUUIDv7()
		if err != nil {
			return task.Request{}, err
		}
	}
	request := task.Request{
		TaskUUID: taskUUID,
		Input:    task.Input{Type: detectInputKind(input), URI: input},
		Output:   task.Output{Root: output},
		Steps: task.StepRequests{
			Subtitles: task.SubtitleRequest{Enabled: subtitles},
			Audio:     task.AudioRequest{Enabled: audio, Strategy: audioStrategy, Package: audioPackage, AAC: audioAAC},
			Video:     task.VideoRequest{Enabled: video, Profiles: splitVideoProfiles(videoProfiles)},
			Sprites: task.SpriteRequest{
				Enabled: sprites, Sizes: splitCSV(spriteSizes), Columns: cfg.SpriteColumns,
				Rows: cfg.SpriteRows, Quality: cfg.SpriteAVIFQuality, Effort: cfg.SpriteAVIFEffort, FrameFormat: strings.ToLower(strings.TrimSpace(spriteFrameFormat)),
			},
		},
	}
	return request, request.Validate()
}

func validateLocalInput(request task.Request) error {
	if request.Input.Type != task.InputLocal {
		return nil
	}
	path, err := filepath.EvalSymlinks(request.Input.URI)
	if err != nil {
		if os.IsNotExist(err) {
			return task.NewError(task.ErrInputNotFound, "local input does not exist", false)
		}
		return task.NewError(task.ErrInputNotReadable, err.Error(), false)
	}
	info, err := os.Stat(path)
	if err != nil {
		return task.NewError(task.ErrInputNotReadable, err.Error(), false)
	}
	if !info.Mode().IsRegular() {
		return task.NewError(task.ErrInputNotReadable, "local input must be a regular file", false)
	}
	file, err := os.Open(path)
	if err != nil {
		return task.NewError(task.ErrInputNotReadable, err.Error(), false)
	}
	return file.Close()
}

func stateStepSpecs(plan pipeline.Plan) []state.StepSpec {
	result := make([]state.StepSpec, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		result = append(result, state.StepSpec{
			Name: step.Name, Kind: step.Kind, Weight: step.Weight,
			MaxAttempts: step.MaxAttempts, Dependencies: step.Dependencies,
		})
	}
	return result
}

func detectInputKind(input string) task.InputKind {
	lower := strings.ToLower(strings.TrimSpace(input))
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return task.InputURL
	}
	return task.InputLocal
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func audioSettingsFromRules(enabled bool, value string) (bool, bool, bool, error) {
	if !enabled {
		return false, false, false, nil
	}
	rules := splitAudioRules(value)
	if len(rules) == 0 {
		return false, false, false, nil
	}
	audioPackage := false
	audioAAC := false
	for _, rule := range rules {
		switch rule {
		case "package":
			audioPackage = true
		case "aac":
			audioAAC = true
		default:
			return false, false, false, task.NewError(task.ErrInvalidTaskSchema, fmt.Sprintf("invalid audio rule %q", rule), false)
		}
	}
	return audioPackage || audioAAC, audioPackage, audioAAC, nil
}

func splitAudioRules(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "package,aac"
	}
	items := splitCSV(value)
	result := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		rule := normalizeAudioRule(item)
		if rule == "" {
			continue
		}
		if rule == "none" {
			return nil
		}
		if seen[rule] {
			continue
		}
		seen[rule] = true
		result = append(result, rule)
	}
	return result
}

func normalizeAudioRulesCSV(value string) string {
	rules := splitAudioRules(value)
	if len(rules) == 0 {
		if isAudioRulesNone(value) {
			return "none"
		}
		return "package,aac"
	}
	return strings.Join(rules, ",")
}

func normalizeAudioRule(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "":
		return ""
	case "package":
		return "package"
	case "aac":
		return "aac"
	case "none":
		return "none"
	default:
		return value
	}
}

func isAudioRulesNone(value string) bool {
	for _, item := range splitCSV(value) {
		if normalizeAudioRule(item) == "none" {
			return true
		}
	}
	return false
}

func displayAudioRules(value string) string {
	normalized := normalizeAudioRulesCSV(value)
	if normalized == "none" {
		return "none"
	}
	return normalized
}

func splitVideoProfiles(value string) []string {
	items := splitCSV(normalizeVideoProfilesCSV(value))
	result := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		profile := normalizeVideoProfile(item)
		if profile == "" || seen[profile] {
			continue
		}
		seen[profile] = true
		result = append(result, profile)
	}
	return result
}

func normalizeVideoProfilesCSV(value string) string {
	profiles := splitCSV(value)
	if len(profiles) == 0 {
		return "package"
	}
	result := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		normalized := normalizeVideoProfile(profile)
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	if len(result) == 0 {
		return "package"
	}
	return strings.Join(result, ",")
}

func normalizeVideoProfile(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return value
}

func displayVideoProfiles(value string) string {
	profiles := splitVideoProfiles(value)
	if len(profiles) == 0 {
		return "package"
	}
	return strings.Join(profiles, ",")
}

func growDelay(current, maxDelay time.Duration) time.Duration {
	if current <= 0 {
		current = time.Second
	}
	current *= 2
	if maxDelay > 0 && current > maxDelay {
		return maxDelay
	}
	return current
}

func sleepContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
