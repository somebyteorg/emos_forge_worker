package app

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"

	"forge_worker/internal/emos"
	"forge_worker/internal/pipeline"
)

func completedStepTimesFromPipeline(steps []pipeline.StepTime) []emos.StepTime {
	result := make([]emos.StepTime, 0, len(steps))
	for _, step := range steps {
		if step.Name == "" {
			continue
		}
		result = append(result, completedStepTime(pipeline.ExternalStepName(step.Name), step.Duration))
	}
	return result
}

func loadCompletedStepTimes(root string) []emos.StepTime {
	data, err := os.ReadFile(filepath.Join(root, "log.json"))
	if err != nil {
		return []emos.StepTime{}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return []emos.StepTime{}
	}
	var log struct {
		Steps []struct {
			Name            string   `json:"name"`
			DurationSeconds *float64 `json:"duration_seconds"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(data, &log); err != nil {
		return []emos.StepTime{}
	}
	result := make([]emos.StepTime, 0, len(log.Steps))
	for _, step := range log.Steps {
		if step.Name == "" || step.DurationSeconds == nil {
			continue
		}
		result = append(result, completedStepTime(pipeline.ExternalStepName(step.Name), *step.DurationSeconds))
	}
	return result
}

func completedStepTime(name string, duration float64) emos.StepTime {
	return emos.StepTime{Name: name, Duration: roundSeconds(duration)}
}

func roundSeconds(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return math.Round(value*1000) / 1000
}
