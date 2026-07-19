package pipeline

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"forge_worker/internal/runner"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func TestFFmpegProgressFraction(t *testing.T) {
	tests := []struct {
		line     string
		duration float64
		want     float64
	}{
		{line: "out_time_us=5000000", duration: 20, want: 0.25},
		{line: "out_time_ms=5000000", duration: 10, want: 0.5},
		{line: "out_time=00:00:07.500000", duration: 15, want: 0.5},
		{line: "progress=end", duration: 15, want: 1},
	}
	for _, tt := range tests {
		got, ok := ffmpegProgressFraction(tt.line, tt.duration)
		if !ok || got != tt.want {
			t.Fatalf("ffmpegProgressFraction(%q) = %v, %v; want %v, true", tt.line, got, ok, tt.want)
		}
	}
}

func TestPercentProgressFraction(t *testing.T) {
	got, ok := percentProgressFraction("vips image: 42.5% complete")
	if !ok || got != 0.425 {
		t.Fatalf("percentProgressFraction = %v, %v; want 0.425, true", got, ok)
	}
}

func TestRunCommandWithProgressUpdatesStepAndTaskProgress(t *testing.T) {
	ctx := context.Background()
	db := openExecutorDB(t)
	request := task.Request{
		TaskUUID: "019f6200-0000-7000-8000-000000000901",
		Input:    task.Input{Type: task.InputLocal, URI: "/source.mkv"},
		Output:   task.Output{Root: t.TempDir()},
	}
	if _, err := db.EnsureTask(ctx, request, task.StateProcessing); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	if err := db.EnsureSteps(ctx, request.TaskUUID, []state.StepSpec{
		{Name: StepVideoGenerate, Kind: "video", Weight: 10, MaxAttempts: 3},
	}); err != nil {
		t.Fatalf("EnsureSteps: %v", err)
	}
	if err := db.StartStep(ctx, request.TaskUUID, StepVideoGenerate); err != nil {
		t.Fatalf("StartStep: %v", err)
	}

	commandRunner := &progressLineRunner{t: t, db: db, taskUUID: request.TaskUUID, stepName: StepVideoGenerate}
	executor := NewExecutor(db, Options{CommandRunner: commandRunner, RetryInitial: time.Millisecond})
	args := []string{"-hide_banner", "-nostdin", "-y", "-i", "input.mkv", "output.mp4"}
	if err := executor.runCommandWithProgress(ctx, request.TaskUUID, StepVideoGenerate, "ffmpeg", args, task.ErrVideoTranscodeFailed, ffmpegCommandProgress(10, 90, 10)); err != nil {
		t.Fatalf("runCommandWithProgress: %v", err)
	}
	if !slices.Contains(commandRunner.args, "-progress") || !slices.Contains(commandRunner.args, "pipe:1") || !slices.Contains(commandRunner.args, "-nostats") {
		t.Fatalf("ffmpeg progress args were not injected: %#v", commandRunner.args)
	}
	if commandRunner.midStepProgress != 50 {
		t.Fatalf("mid step progress = %v, want 50", commandRunner.midStepProgress)
	}
	step := findStepRecord(t, db, request.TaskUUID, StepVideoGenerate)
	if step.Progress != 90 {
		t.Fatalf("final command step progress = %v, want 90", step.Progress)
	}
	if step.FPS != 3.9 || step.Speed != 0.15 {
		t.Fatalf("final command performance = %.1f fps %.2fx, want 3.9 fps 0.15x", step.FPS, step.Speed)
	}
	if strings.Contains(step.CommandSummary, "-progress") || strings.Contains(step.CommandSummary, "pipe:1") || strings.Contains(step.CommandSummary, "-nostats") {
		t.Fatalf("command summary leaked internal progress args: %s", step.CommandSummary)
	}
	record, err := db.GetTask(ctx, request.TaskUUID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if record.Progress != 90 {
		t.Fatalf("task progress = %v, want 90", record.Progress)
	}
}

type progressLineRunner struct {
	t               *testing.T
	db              *state.DB
	taskUUID        string
	stepName        string
	args            []string
	midStepProgress float64
}

func (r *progressLineRunner) Run(_ context.Context, spec runner.Spec) (runner.Result, error) {
	r.args = append([]string(nil), spec.Args...)
	if spec.OnLine != nil {
		spec.OnLine("stdout", "fps=3.90")
		spec.OnLine("stdout", "speed=0.150x")
		spec.OnLine("stdout", "out_time_us=5000000")
		spec.OnLine("stdout", "progress=continue")
		step := findStepRecord(r.t, r.db, r.taskUUID, r.stepName)
		r.midStepProgress = step.Progress
	}
	return runner.Result{ExitCode: 0, Started: time.Now().UTC(), Finished: time.Now().UTC()}, nil
}

func TestProcessingManifestIncludesFFmpegPerformance(t *testing.T) {
	result := processingManifest(state.TaskRecord{}, time.Now(), []state.StepRecord{{
		Name: StepVideoGenerate, Kind: "video", State: string(task.StepSucceeded), Progress: 100, FPS: 3.9, Speed: 0.15,
		DetailsJSON: `{"reason":"profile skipped"}`,
	}}, nil, nil, Options{})
	steps, ok := result["steps"].([]map[string]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("processing steps = %#v", result["steps"])
	}
	if steps[0]["name"] != "video_generate" {
		t.Fatalf("processing step name = %#v", steps[0]["name"])
	}
	if steps[0]["fps"] != 3.9 || steps[0]["speed"] != 0.15 {
		t.Fatalf("processing performance = %#v", steps[0])
	}
	details, ok := steps[0]["details"].(map[string]any)
	if !ok || details["reason"] != "profile skipped" {
		t.Fatalf("processing details = %#v", steps[0]["details"])
	}
}

func TestStepTimesFromFinalizedSteps(t *testing.T) {
	started := time.Date(2026, 7, 19, 12, 0, 0, 123000000, time.UTC)
	finished := started.Add(1534 * time.Millisecond)

	got := stepTimesFromFinalizedSteps([]state.StepRecord{
		{Name: StepVideoPackage, StartedAt: &started, FinishedAt: &finished},
		{Name: "pending"},
		{Name: "zero", StartedAt: &started, FinishedAt: &started},
	})

	if len(got) != 2 {
		t.Fatalf("step times = %+v", got)
	}
	if got[0].Name != "video_package" || got[0].Duration != 1.534 {
		t.Fatalf("first step time = %+v", got[0])
	}
	if got[1].Name != "zero" || got[1].Duration != 0 {
		t.Fatalf("second step time = %+v", got[1])
	}
}
