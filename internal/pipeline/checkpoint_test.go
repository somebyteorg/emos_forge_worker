package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"forge_worker/internal/task"
)

func TestExecutorRecoverySkipsCompletedPipeline(t *testing.T) {
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := subtitleExecutorRequest(t, input)

	firstDB := openExecutorDB(t)
	ensureExecutorPlan(t, firstDB, request)
	firstRunner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(firstDB, Options{
		EnableRecovery: true,
		ProbeRunner:    fakeProbeRunner{stdout: subtitleProbeJSON()},
		CommandRunner:  firstRunner,
		RetryInitial:   time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if len(firstRunner.ffmpegSubtitleArgs) != 1 {
		t.Fatalf("first run subtitle commands = %d", len(firstRunner.ffmpegSubtitleArgs))
	}
	checkpointPath := filepath.Join(taskRoot(request), "tmp", pipelineCheckpointName)
	if _, err := os.Stat(checkpointPath); err != nil {
		t.Fatalf("worker checkpoint missing: %v", err)
	}

	secondDB := openExecutorDB(t)
	ensureExecutorPlan(t, secondDB, request)
	secondRunner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	var recovery RecoveryInfo
	var stepTimes []StepTime
	if err := NewExecutor(secondDB, Options{
		EnableRecovery: true,
		ProbeRunner:    fakeProbeRunner{},
		CommandRunner:  secondRunner,
		RetryInitial:   time.Millisecond,
		OnRecovery:     func(info RecoveryInfo) { recovery = info },
		OnStepTimes:    func(value []StepTime) { stepTimes = append([]StepTime(nil), value...) },
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("recovered Run: %v", err)
	}
	if len(secondRunner.ffmpegSubtitleArgs) != 0 {
		t.Fatalf("recovered pipeline reran subtitle command: %#v", secondRunner.ffmpegSubtitleArgs)
	}
	if !recovery.CheckpointFound || len(recovery.RecoveredSteps) != 6 || recovery.InvalidStep != "" {
		t.Fatalf("recovery info = %+v", recovery)
	}
	if len(stepTimes) != 6 {
		t.Fatalf("recovered step times = %+v", stepTimes)
	}
	record, err := secondDB.GetTask(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if record.State != task.StateSucceeded {
		t.Fatalf("recovered task state = %s", record.State)
	}
}

func TestExecutorRecoverySkipsCompletedAVCommands(t *testing.T) {
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := executorRequest(t, input)
	firstDB := openExecutorDB(t)
	ensureExecutorPlan(t, firstDB, request)
	firstRunner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(firstDB, Options{
		EnableRecovery: true,
		ProbeRunner:    fakeProbeRunner{stdout: probeJSON(false)},
		CommandRunner:  firstRunner,
		RetryInitial:   time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("first AV Run: %v", err)
	}
	if firstRunner.ffmpegCommands == 0 || len(firstRunner.packagerArgs) == 0 {
		t.Fatal("first AV run did not invoke media commands")
	}
	if _, err := os.Stat(filepath.Join(taskRoot(request), "tmp", "video", "video_package.mp4")); err != nil {
		t.Fatalf("worker AV intermediate missing before completed: %v", err)
	}

	secondDB := openExecutorDB(t)
	ensureExecutorPlan(t, secondDB, request)
	secondRunner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(secondDB, Options{
		EnableRecovery: true,
		ProbeRunner:    fakeProbeRunner{},
		CommandRunner:  secondRunner,
		RetryInitial:   time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("recovered AV Run: %v", err)
	}
	if secondRunner.ffmpegCommands != 0 || len(secondRunner.packagerArgs) != 0 {
		t.Fatalf("recovered AV pipeline invoked commands: ffmpeg=%d packager=%d", secondRunner.ffmpegCommands, len(secondRunner.packagerArgs))
	}
}

func TestExecutorRecoveryRerunsOnlyFromInvalidArtifact(t *testing.T) {
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := subtitleExecutorRequest(t, input)
	firstDB := openExecutorDB(t)
	ensureExecutorPlan(t, firstDB, request)
	if err := NewExecutor(firstDB, Options{
		EnableRecovery: true,
		ProbeRunner:    fakeProbeRunner{stdout: subtitleProbeJSON()},
		CommandRunner:  &fakeCommandRunner{t: t},
		RetryInitial:   time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	subtitlePath := filepath.Join(taskRoot(request), "subtitles", "sub_03_eng.vtt")
	subtitleBefore, err := os.ReadFile(subtitlePath)
	if err != nil {
		t.Fatalf("read subtitle before recovery: %v", err)
	}
	manifestPath := filepath.Join(taskRoot(request), "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("invalid"), 0o600); err != nil {
		t.Fatalf("corrupt manifest: %v", err)
	}

	secondDB := openExecutorDB(t)
	ensureExecutorPlan(t, secondDB, request)
	secondRunner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	var recovery RecoveryInfo
	if err := NewExecutor(secondDB, Options{
		EnableRecovery: true,
		ProbeRunner:    fakeProbeRunner{},
		CommandRunner:  secondRunner,
		RetryInitial:   time.Millisecond,
		OnRecovery:     func(info RecoveryInfo) { recovery = info },
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("recovered Run: %v", err)
	}
	if recovery.InvalidStep != StepFinalize || recovery.InvalidArtifact != "manifest.json" {
		t.Fatalf("recovery info = %+v", recovery)
	}
	if len(secondRunner.ffmpegSubtitleArgs) != 0 {
		t.Fatalf("recovery reran completed subtitle output: %#v", secondRunner.ffmpegSubtitleArgs)
	}
	subtitleAfter, err := os.ReadFile(subtitlePath)
	if err != nil {
		t.Fatalf("read subtitle after recovery: %v", err)
	}
	if string(subtitleAfter) != string(subtitleBefore) {
		t.Fatalf("subtitle changed during finalize-only recovery")
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest was not regenerated: %v", err)
	}
}

func TestExecutorRecoveryDoesNotReuseOutputsAfterInputChanges(t *testing.T) {
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := subtitleExecutorRequest(t, input)
	firstDB := openExecutorDB(t)
	ensureExecutorPlan(t, firstDB, request)
	if err := NewExecutor(firstDB, Options{
		EnableRecovery: true,
		ProbeRunner:    fakeProbeRunner{stdout: subtitleProbeJSON()},
		CommandRunner:  &fakeCommandRunner{t: t},
		RetryInitial:   time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := os.WriteFile(input, []byte("changed media input"), 0o600); err != nil {
		t.Fatalf("replace input: %v", err)
	}

	secondDB := openExecutorDB(t)
	ensureExecutorPlan(t, secondDB, request)
	secondRunner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	var recovery RecoveryInfo
	if err := NewExecutor(secondDB, Options{
		EnableRecovery: true,
		ProbeRunner:    fakeProbeRunner{stdout: subtitleProbeJSON()},
		CommandRunner:  secondRunner,
		RetryInitial:   time.Millisecond,
		OnRecovery:     func(info RecoveryInfo) { recovery = info },
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run after input change: %v", err)
	}
	if recovery.InvalidStep != StepPrepare || recovery.InvalidReason != "input file changed" || len(recovery.RecoveredSteps) != 0 {
		t.Fatalf("recovery info = %+v", recovery)
	}
	if len(secondRunner.ffmpegSubtitleArgs) != 1 {
		t.Fatalf("changed input should regenerate subtitles, commands=%d", len(secondRunner.ffmpegSubtitleArgs))
	}
}
