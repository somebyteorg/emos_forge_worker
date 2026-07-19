package app

import (
	"slices"
	"strings"
	"testing"

	"forge_worker/internal/config"
	"forge_worker/internal/emos"
	"forge_worker/internal/pipeline"
)

func TestTranslateEMOSJobStepsIncludesSubtitlePackage(t *testing.T) {
	selection, err := translateEMOSJobSteps([]emos.JobStep{
		emos.JobStepSubtitlePackage,
		emos.JobStepVideo720P,
		emos.JobStepVideo1080P,
		emos.JobStepVideoPackage,
		emos.JobStepAudioPackage,
		emos.JobStepAudioAAC,
		emos.JobStepSubtitlePackage,
		emos.JobStepSprite320,
		emos.JobStepSprite640,
		emos.JobStepSprite720,
	})
	if err != nil {
		t.Fatalf("translateEMOSJobSteps: %v", err)
	}
	if !selection.SubtitlePackage {
		t.Fatal("subtitle_package should enable subtitle extraction")
	}
	if !slices.Equal(selection.VideoProfiles, []string{"720p", "1080p", "package"}) {
		t.Fatalf("video profiles = %v", selection.VideoProfiles)
	}
	if !selection.AudioPackage || !selection.AudioAAC {
		t.Fatalf("audio selection = package:%t aac:%t", selection.AudioPackage, selection.AudioAAC)
	}
	if !slices.Equal(selection.SpriteSizes, []string{"320x180", "640x360", "1280x720"}) {
		t.Fatalf("sprite sizes = %v", selection.SpriteSizes)
	}
}

func TestCompletedStepTimesPreserveSubtitlePackageName(t *testing.T) {
	steps := completedStepTimesFromPipeline([]pipeline.StepTime{{Name: pipeline.StepSubtitlePackage, Duration: 1.2345}})
	if len(steps) != 1 || steps[0].Name != string(emos.JobStepSubtitlePackage) || steps[0].Duration != 1.235 {
		t.Fatalf("completed step times = %+v", steps)
	}
}

func TestTranslateEMOSJobStepsRejectsLegacySubtitleName(t *testing.T) {
	_, err := translateEMOSJobSteps([]emos.JobStep{"subtitles_extract"})
	if err == nil || !strings.Contains(err.Error(), "unsupported job step") {
		t.Fatalf("expected unsupported legacy subtitle step, got %v", err)
	}
}

func TestBuildEMOSRequestCreatesSubtitlePackagePlan(t *testing.T) {
	input := "/media/source.mkv"
	request, err := buildEMOSRequest(config.Config{OutputDir: "/project/output"}, "019f61e1-eb9d-7a90-adba-3a6f7ecc8611", emos.JobInfo{
		FilePath: &input,
		JobSteps: []emos.JobStep{emos.JobStepSubtitlePackage},
	})
	if err != nil {
		t.Fatalf("buildEMOSRequest: %v", err)
	}
	if !request.Steps.Subtitles.Enabled {
		t.Fatal("subtitle_package should enable subtitles in the pipeline request")
	}
	if request.Steps.Audio.Enabled || request.Steps.Video.Enabled || request.Steps.Sprites.Enabled {
		t.Fatalf("subtitle-only job enabled unrelated steps: %+v", request.Steps)
	}
	plan, err := pipeline.Build(request, 3)
	if err != nil {
		t.Fatalf("pipeline.Build: %v", err)
	}
	found := false
	for _, step := range plan.Steps {
		if step.Name == pipeline.StepSubtitlePackage {
			found = true
		}
		if step.Name == "subtitles_extract" {
			t.Fatalf("legacy subtitle step leaked into plan: %+v", plan.Steps)
		}
	}
	if !found {
		t.Fatalf("plan does not contain %s: %+v", pipeline.StepSubtitlePackage, plan.Steps)
	}
}
