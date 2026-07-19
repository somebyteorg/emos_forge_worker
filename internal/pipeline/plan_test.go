package pipeline

import (
	"path/filepath"
	"slices"
	"testing"

	"forge_worker/internal/task"
)

func TestBuildURLTaskPlan(t *testing.T) {
	plan, err := Build(task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8601",
		Input:    task.Input{Type: task.InputURL, URI: "https://example.test/movie.mkv"},
		Output:   task.Output{Root: absTestPath(t, filepath.Join("..", "..", "output", "plan-output"))},
		Steps: task.StepRequests{
			Subtitles: task.SubtitleRequest{Enabled: true},
			Audio:     task.AudioRequest{Enabled: true, Strategy: "one_per_language"},
			Video:     task.VideoRequest{Enabled: true, Profiles: []string{"720p", "1080p"}},
			Sprites:   task.SpriteRequest{Enabled: true, Sizes: []string{"320x180"}, Columns: 10, Rows: 10, Quality: 70, Effort: 4},
		},
	}, 3)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	names := stepNames(plan)
	for _, name := range []string{StepPrepare, StepDownloadURL, StepProbe, StepValidateInput, StepSubtitlePackage, StepVideoPackage, StepVideoGenerate, StepAudioSelect, StepSpritesGenerate, StepAudioPackage, StepValidateOutput, StepFinalize} {
		if !slices.Contains(names, name) {
			t.Fatalf("plan missing %s: %v", name, names)
		}
	}
	if slices.Contains(names, "video.720p") || slices.Contains(names, "video.1080p") {
		t.Fatalf("generated video profiles should be bundled into %s: %v", StepVideoGenerate, names)
	}
	if plan.TotalWeight() <= 0 {
		t.Fatalf("expected positive total weight")
	}
	spriteStep := findStep(t, plan, StepSpritesGenerate)
	if len(spriteStep.Dependencies) != 1 || spriteStep.Dependencies[0] != StepAudioPackage {
		t.Fatalf("sprites should run after packaged AV: %+v", spriteStep.Dependencies)
	}
	audioStep := findStep(t, plan, StepAudioSelect)
	if len(audioStep.Dependencies) != 1 || audioStep.Dependencies[0] != StepVideoGenerate {
		t.Fatalf("audio should run after generated video when requested together: %+v", audioStep.Dependencies)
	}
	subtitleStep := findStep(t, plan, StepSubtitlePackage)
	if len(subtitleStep.Dependencies) != 1 || subtitleStep.Dependencies[0] != StepValidateInput {
		t.Fatalf("subtitles should run before video after input validation: %+v", subtitleStep.Dependencies)
	}
	videoStep := findStep(t, plan, StepVideoGenerate)
	if len(videoStep.Dependencies) != 1 || videoStep.Dependencies[0] != StepVideoPackage {
		t.Fatalf("generated video should run after package source preparation: %+v", videoStep.Dependencies)
	}
	videoPackageStep := findStep(t, plan, StepVideoPackage)
	if len(videoPackageStep.Dependencies) != 1 || videoPackageStep.Dependencies[0] != StepSubtitlePackage {
		t.Fatalf("package source should run after subtitles when requested together: %+v", videoPackageStep.Dependencies)
	}
	packageStep := findStep(t, plan, StepAudioPackage)
	if len(packageStep.Dependencies) != 1 || packageStep.Dependencies[0] != StepAudioSelect {
		t.Fatalf("package should run after audio stage: %+v", packageStep.Dependencies)
	}
	validateStep := findStep(t, plan, StepValidateOutput)
	if len(validateStep.Dependencies) != 1 || validateStep.Dependencies[0] != StepSpritesGenerate {
		t.Fatalf("validate output should wait for sprites: %+v", validateStep.Dependencies)
	}
}

func TestBuildAudioAACPlan(t *testing.T) {
	plan, err := Build(task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8601",
		Input:    task.Input{Type: task.InputLocal, URI: absTestPath(t, filepath.Join("..", "..", "output", "source.mkv"))},
		Output:   task.Output{Root: absTestPath(t, filepath.Join("..", "..", "output", "plan-output"))},
		Steps: task.StepRequests{
			Audio: task.AudioRequest{Enabled: true, Strategy: "one_per_language", AAC: true},
		},
	}, 3)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	audioAAC := findStep(t, plan, StepAudioAAC)
	if len(audioAAC.Dependencies) != 1 || audioAAC.Dependencies[0] != StepAudioSelect {
		t.Fatalf("%s dependencies = %+v, want %s", StepAudioAAC, audioAAC.Dependencies, StepAudioSelect)
	}
	packageStep := findStep(t, plan, StepAudioPackage)
	if len(packageStep.Dependencies) != 1 || packageStep.Dependencies[0] != StepAudioAAC {
		t.Fatalf("package should wait for AAC filter: %+v", packageStep.Dependencies)
	}
}

func TestBuildPackageVideoPlan(t *testing.T) {
	plan, err := Build(task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8601",
		Input:    task.Input{Type: task.InputLocal, URI: absTestPath(t, filepath.Join("..", "..", "output", "source.mkv"))},
		Output:   task.Output{Root: absTestPath(t, filepath.Join("..", "..", "output", "plan-output"))},
		Steps: task.StepRequests{
			Audio: task.AudioRequest{Enabled: true, Strategy: "one_per_language"},
			Video: task.VideoRequest{Enabled: true, Profiles: []string{"package"}},
		},
	}, 3)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	names := stepNames(plan)
	if !slices.Contains(names, StepVideoPackage) {
		t.Fatalf("plan missing package video step: %v", names)
	}
	audioStep := findStep(t, plan, StepAudioSelect)
	if len(audioStep.Dependencies) != 1 || audioStep.Dependencies[0] != StepVideoPackage {
		t.Fatalf("audio should depend on %s: %+v", StepVideoPackage, audioStep.Dependencies)
	}
	packageStep := findStep(t, plan, StepAudioPackage)
	if len(packageStep.Dependencies) != 1 || packageStep.Dependencies[0] != StepAudioSelect {
		t.Fatalf("package should run after audio stage: %+v", packageStep.Dependencies)
	}
}

func TestBuildGeneratedVideoPlanAlwaysGeneratesFromPackage(t *testing.T) {
	plan, err := Build(task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8601",
		Input:    task.Input{Type: task.InputLocal, URI: absTestPath(t, filepath.Join("..", "..", "output", "source.mkv"))},
		Output:   task.Output{Root: absTestPath(t, filepath.Join("..", "..", "output", "plan-output"))},
		Steps: task.StepRequests{
			Video: task.VideoRequest{Enabled: true, Profiles: []string{"720p"}},
		},
	}, 3)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	generateStep := findStep(t, plan, StepVideoGenerate)
	if !slices.Contains(generateStep.Dependencies, StepVideoPackage) {
		t.Fatalf("%s should always depend on %s: %+v", StepVideoGenerate, StepVideoPackage, generateStep.Dependencies)
	}
	if slices.Contains(generateStep.Dependencies, StepValidateInput) {
		t.Fatalf("%s should wait for package instead of validate_input directly: %+v", StepVideoGenerate, generateStep.Dependencies)
	}
}

func TestBuildLocalSubtitleOnlyPlanSkipsPackage(t *testing.T) {
	plan, err := Build(task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8601",
		Input:    task.Input{Type: task.InputLocal, URI: absTestPath(t, filepath.Join("..", "..", "output", "source.mkv"))},
		Output:   task.Output{Root: absTestPath(t, filepath.Join("..", "..", "output", "plan-output"))},
		Steps:    task.StepRequests{Subtitles: task.SubtitleRequest{Enabled: true}},
	}, 3)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	names := stepNames(plan)
	if slices.Contains(names, StepDownloadURL) || slices.Contains(names, StepAudioPackage) {
		t.Fatalf("unexpected download/package in subtitle-only local plan: %v", names)
	}
	finalize := findStep(t, plan, StepFinalize)
	if len(finalize.Dependencies) != 1 || finalize.Dependencies[0] != StepValidateOutput {
		t.Fatalf("unexpected finalize dependencies: %+v", finalize.Dependencies)
	}
}

func stepNames(plan Plan) []string {
	result := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		result = append(result, step.Name)
	}
	return result
}

func findStep(t *testing.T, plan Plan, name string) Step {
	t.Helper()
	for _, step := range plan.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("missing step %s", name)
	return Step{}
}

func absTestPath(t *testing.T, rel string) string {
	t.Helper()
	path, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", rel, err)
	}
	return path
}
