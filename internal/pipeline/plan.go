package pipeline

import (
	"fmt"

	"forge_worker/internal/task"
)

const (
	StepPrepare         = "prepare"
	StepDownloadURL     = "download_url"
	StepProbe           = "probe"
	StepValidateInput   = "validate_input"
	StepSubtitlePackage = "subtitle_package"
	StepVideoPackage    = "video_package"
	StepVideoGenerate   = "video_generate"
	StepAudioSelect     = "audio_select"
	StepAudioAAC        = "audio_aac"
	StepAudioPackage    = "audio_package"
	StepSpritesGenerate = "sprites_generate"
	StepValidateOutput  = "validate_output"
	StepFinalize        = "finalize"
)

type Step struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Weight       float64  `json:"weight"`
	MaxAttempts  int      `json:"max_attempts"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type Plan struct {
	TaskUUID string `json:"task_uuid"`
	Steps    []Step `json:"steps"`
}

func Build(request task.Request, maxAttempts int) (Plan, error) {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if err := request.Validate(); err != nil {
		return Plan{}, err
	}
	b := builder{maxAttempts: maxAttempts}
	b.add(Step{Name: StepPrepare, Kind: "prepare", Weight: 2})
	probeDeps := []string{StepPrepare}
	if request.Input.Type == task.InputURL {
		b.add(Step{Name: StepDownloadURL, Kind: "download", Weight: 10, Dependencies: []string{StepPrepare}})
		probeDeps = []string{StepDownloadURL}
	}
	b.add(Step{Name: StepProbe, Kind: "probe", Weight: 2, Dependencies: probeDeps})
	b.add(Step{Name: StepValidateInput, Kind: "validate", Weight: 1, Dependencies: []string{StepProbe}})

	stageDeps := []string{StepValidateInput}
	terminalDeps := append([]string(nil), stageDeps...)
	avEnabled := false

	if request.Steps.Subtitles.Enabled {
		name := StepSubtitlePackage
		b.add(Step{Name: name, Kind: "subtitles", Weight: 5, Dependencies: append([]string(nil), stageDeps...)})
		stageDeps = []string{name}
		terminalDeps = append([]string(nil), stageDeps...)
	}

	if request.Steps.Video.Enabled {
		videoSteps := videoStepNames(request.Steps.Video.Profiles)
		for _, name := range videoSteps {
			weight := 45.0
			if len(videoSteps) > 1 {
				weight = 40
				if name == StepVideoPackage {
					weight = 5
				}
			}
			b.add(Step{Name: name, Kind: "video", Weight: weight, Dependencies: append([]string(nil), stageDeps...)})
			stageDeps = []string{name}
		}
		terminalDeps = append([]string(nil), stageDeps...)
		avEnabled = true
	}

	if request.Steps.Audio.Enabled {
		weight := 15.0
		if request.Steps.Audio.AAC {
			weight = 5
		}
		b.add(Step{Name: StepAudioSelect, Kind: "audio", Weight: weight, Dependencies: append([]string(nil), stageDeps...)})
		stageDeps = []string{StepAudioSelect}
		if request.Steps.Audio.AAC {
			b.add(Step{Name: StepAudioAAC, Kind: "audio", Weight: 10, Dependencies: []string{StepAudioSelect}})
			stageDeps = []string{StepAudioAAC}
		}
		terminalDeps = append([]string(nil), stageDeps...)
		avEnabled = true
	}

	if avEnabled {
		b.add(Step{Name: StepAudioPackage, Kind: "package", Weight: 7, Dependencies: append([]string(nil), stageDeps...)})
		stageDeps = []string{StepAudioPackage}
		terminalDeps = append([]string(nil), stageDeps...)
	}

	if request.Steps.Sprites.Enabled {
		name := StepSpritesGenerate
		b.add(Step{Name: name, Kind: "sprites", Weight: 10, Dependencies: append([]string(nil), stageDeps...)})
		stageDeps = []string{name}
		terminalDeps = append([]string(nil), stageDeps...)
	}
	b.add(Step{Name: StepValidateOutput, Kind: "validate", Weight: 1.5, Dependencies: terminalDeps})
	b.add(Step{Name: StepFinalize, Kind: "finalize", Weight: 1.5, Dependencies: []string{StepValidateOutput}})
	plan := Plan{TaskUUID: request.TaskUUID, Steps: b.steps}
	if err := plan.Validate(); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (p Plan) Validate() error {
	seen := make(map[string]bool, len(p.Steps))
	for _, step := range p.Steps {
		if step.Name == "" || step.Kind == "" {
			return fmt.Errorf("pipeline step name and kind are required")
		}
		if seen[step.Name] {
			return fmt.Errorf("duplicate pipeline step %q", step.Name)
		}
		seen[step.Name] = true
		if step.Weight <= 0 || step.MaxAttempts <= 0 {
			return fmt.Errorf("pipeline step %q has invalid scheduling values", step.Name)
		}
	}
	for _, step := range p.Steps {
		for _, dependency := range step.Dependencies {
			if !seen[dependency] {
				return fmt.Errorf("pipeline step %q depends on unknown step %q", step.Name, dependency)
			}
		}
	}
	return nil
}

func (p Plan) TotalWeight() float64 {
	var total float64
	for _, step := range p.Steps {
		total += step.Weight
	}
	return total
}

type builder struct {
	steps       []Step
	maxAttempts int
}

func (b *builder) add(step Step) {
	step.MaxAttempts = b.maxAttempts
	b.steps = append(b.steps, step)
}

func videoStepNames(profiles []string) []string {
	hasGenerate := false
	for _, profile := range profiles {
		if profile != "package" {
			hasGenerate = true
		}
	}
	names := []string{StepVideoPackage}
	if hasGenerate {
		names = append(names, StepVideoGenerate)
	}
	return names
}

func videoProfilesInclude(profiles []string, profile string) bool {
	for _, candidate := range profiles {
		if candidate == profile {
			return true
		}
	}
	return false
}
