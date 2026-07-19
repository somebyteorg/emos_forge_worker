package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func TestIntegrationRealToolsPipeline(t *testing.T) {
	if os.Getenv("FORGE_INTEGRATION") != "1" {
		t.Skip("set FORGE_INTEGRATION=1 to run real ffmpeg/packager/vips pipeline test")
	}
	input := os.Getenv("FORGE_INTEGRATION_INPUT")
	if input == "" {
		t.Skip("set FORGE_INTEGRATION_INPUT to an absolute media path")
	}
	if _, err := os.Stat(input); err != nil {
		t.Skipf("integration input is unavailable: %v", err)
	}
	profile := os.Getenv("FORGE_INTEGRATION_VIDEO_PROFILE")
	if profile == "" {
		profile = "package"
	}
	sprites := os.Getenv("FORGE_INTEGRATION_SPRITES") != "0"
	audioAAC := os.Getenv("FORGE_INTEGRATION_AUDIO_AAC") == "1"
	db := state.New()
	request := task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc86aa",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps: task.StepRequests{
			Subtitles: task.SubtitleRequest{Enabled: os.Getenv("FORGE_INTEGRATION_SUBTITLES") != "0"},
			Audio:     task.AudioRequest{Enabled: true, Strategy: "one_per_language", Package: true, AAC: audioAAC},
			Video:     task.VideoRequest{Enabled: true, Profiles: []string{profile}},
			Sprites:   task.SpriteRequest{Enabled: sprites, Sizes: []string{"320x180"}, Columns: 5, Rows: 4, Quality: 55, Effort: 1},
		},
	}
	if _, err := db.EnsureTask(context.Background(), request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	plan, err := Build(request, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var specs []state.StepSpec
	for _, step := range plan.Steps {
		specs = append(specs, state.StepSpec{Name: step.Name, Kind: step.Kind, Weight: step.Weight, MaxAttempts: step.MaxAttempts, Dependencies: step.Dependencies})
	}
	if err := db.EnsureSteps(context.Background(), request.TaskUUID, specs); err != nil {
		t.Fatalf("EnsureSteps: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := NewExecutor(db, Options{CPULimit: integrationCPULimit()}).Run(ctx, request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json")); err != nil {
		t.Fatalf("manifest.json missing: %v", err)
	}
}

func integrationCPULimit() int {
	if value := os.Getenv("FORGE_CPU_LIMIT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return max(2, runtime.NumCPU())
}
