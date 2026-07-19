package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"forge_worker/internal/task"
)

func TestEnsureTaskRejectsDifferentRequestForExistingTask(t *testing.T) {
	db := New()
	request := testRequest()
	created, err := db.EnsureTask(context.Background(), request, task.StateDiscovered)
	if err != nil {
		t.Fatalf("EnsureTask initial: %v", err)
	}
	if !created {
		t.Fatalf("expected initial request to create task")
	}

	created, err = db.EnsureTask(context.Background(), request, task.StateDiscovered)
	if err != nil {
		t.Fatalf("EnsureTask identical: %v", err)
	}
	if created {
		t.Fatalf("expected identical request to reuse task")
	}

	changed := request
	changed.Input.URI = "/var/forge-worker-test/other.mov"
	_, err = db.EnsureTask(context.Background(), changed, task.StateDiscovered)
	var taskErr *task.Error
	if !errors.As(err, &taskErr) || taskErr.Code != task.ErrTaskOutputConflict {
		t.Fatalf("expected TASK_OUTPUT_CONFLICT, got %v", err)
	}
}

func TestStepLifecycleAndReadyDependencies(t *testing.T) {
	db := New()
	request := testRequest()
	if _, err := db.EnsureTask(context.Background(), request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	specs := []StepSpec{
		{Name: "prepare", Kind: "prepare", Weight: 2, MaxAttempts: 3},
		{Name: "probe", Kind: "probe", Weight: 2, MaxAttempts: 3, Dependencies: []string{"prepare"}},
	}
	if err := db.EnsureSteps(context.Background(), request.TaskUUID, specs); err != nil {
		t.Fatalf("EnsureSteps: %v", err)
	}
	if err := db.EnsureSteps(context.Background(), request.TaskUUID, specs); err != nil {
		t.Fatalf("EnsureSteps idempotent: %v", err)
	}
	ready, err := db.ListReadySteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListReadySteps: %v", err)
	}
	if len(ready) != 1 || ready[0].Name != "prepare" {
		t.Fatalf("unexpected initial ready steps: %+v", ready)
	}
	if err := db.StartStep(context.Background(), request.TaskUUID, "prepare"); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if err := db.UpdateStepProgress(context.Background(), request.TaskUUID, "prepare", 42); err != nil {
		t.Fatalf("UpdateStepProgress: %v", err)
	}
	if err := db.UpdateStepPerformance(context.Background(), request.TaskUUID, "prepare", 3.9, 0.15); err != nil {
		t.Fatalf("UpdateStepPerformance: %v", err)
	}
	if err := db.CompleteStep(context.Background(), request.TaskUUID, "prepare"); err != nil {
		t.Fatalf("CompleteStep: %v", err)
	}
	ready, err = db.ListReadySteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListReadySteps after prepare: %v", err)
	}
	if len(ready) != 1 || ready[0].Name != "probe" {
		t.Fatalf("unexpected ready steps after prepare: %+v", ready)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if steps[0].State != string(task.StepSucceeded) || steps[0].Progress != 100 || steps[0].Attempt != 1 || steps[0].FPS != 3.9 || steps[0].Speed != 0.15 {
		t.Fatalf("unexpected completed step record: %+v", steps[0])
	}
}

func TestTaskProgressProbeAndFinish(t *testing.T) {
	db := New()
	request := testRequest()
	ctx := context.Background()
	if _, err := db.EnsureTask(ctx, request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	for _, next := range []task.State{
		task.StatePreparing, task.StateProbing, task.StateValidating, task.StateProcessing,
		task.StatePackaging, task.StateValidatingOutput, task.StateFinalizing,
	} {
		if err := db.TransitionTaskTo(ctx, request.TaskUUID, next); err != nil {
			t.Fatalf("TransitionTaskTo %s: %v", next, err)
		}
	}
	if err := db.SetTaskProbe(ctx, request.TaskUUID, map[string]any{"duration": 12}); err != nil {
		t.Fatalf("SetTaskProbe: %v", err)
	}
	if err := db.FinishTask(ctx, request.TaskUUID, task.StateSucceeded); err != nil {
		t.Fatalf("FinishTask: %v", err)
	}
	record, err := db.GetTask(ctx, request.TaskUUID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if record.State != task.StateSucceeded || record.Progress != 100 || !strings.Contains(record.ProbeJSON, "duration") {
		t.Fatalf("unexpected finished task: %+v", record)
	}
}

func TestEnsureStepsDoesNotPartiallyApplyInvalidPlan(t *testing.T) {
	db := New()
	request := testRequest()
	ctx := context.Background()
	if _, err := db.EnsureTask(ctx, request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	err := db.EnsureSteps(ctx, request.TaskUUID, []StepSpec{
		{Name: "prepare", Kind: "prepare", Weight: 1, MaxAttempts: 1},
		{Name: "probe", Kind: "probe", Weight: 1, MaxAttempts: 1, Dependencies: []string{"missing"}},
	})
	if err == nil {
		t.Fatalf("expected invalid dependency to fail")
	}
	steps, listErr := db.ListSteps(ctx, request.TaskUUID)
	if listErr != nil {
		t.Fatalf("ListSteps: %v", listErr)
	}
	if len(steps) != 0 {
		t.Fatalf("invalid plan left partial steps: %+v", steps)
	}
}

func TestArtifactUpsertListDeleteAndPathValidation(t *testing.T) {
	db := New()
	request := testRequest()
	if _, err := db.EnsureTask(context.Background(), request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	if err := db.UpsertArtifact(context.Background(), request.TaskUUID, ArtifactSpec{
		StepName:     "audio_select",
		Kind:         "audio_intermediate",
		RelativePath: "tmp/audio/track_1.m4a",
		SizeBytes:    12,
		Committed:    true,
		Metadata:     map[string]any{"language": "eng"},
	}); err != nil {
		t.Fatalf("UpsertArtifact: %v", err)
	}
	if err := db.UpsertArtifact(context.Background(), request.TaskUUID, ArtifactSpec{
		StepName:     "audio_select",
		Kind:         "audio_intermediate",
		RelativePath: "tmp/audio/track_1.m4a",
		SizeBytes:    34,
		Committed:    true,
	}); err != nil {
		t.Fatalf("UpsertArtifact update: %v", err)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].SizeBytes != 34 || !artifacts[0].Committed {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
	if err := db.DeleteArtifact(context.Background(), request.TaskUUID, "tmp/audio/track_1.m4a"); err != nil {
		t.Fatalf("DeleteArtifact: %v", err)
	}
	artifacts, err = db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts after delete: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected artifact deletion, got %+v", artifacts)
	}
	if err := db.UpsertArtifact(context.Background(), request.TaskUUID, ArtifactSpec{
		StepName: "bad", Kind: "bad", RelativePath: "../escape", SizeBytes: 1,
	}); err == nil {
		t.Fatalf("expected escaped artifact path to fail")
	}
}

func TestWarningsCommandSummaryAndRetry(t *testing.T) {
	db := New()
	request := testRequest()
	if _, err := db.EnsureTask(context.Background(), request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	if err := db.EnsureSteps(context.Background(), request.TaskUUID, []StepSpec{{Name: "probe", Kind: "probe", Weight: 1, MaxAttempts: 3}}); err != nil {
		t.Fatalf("EnsureSteps: %v", err)
	}
	if err := db.StartStep(context.Background(), request.TaskUUID, "probe"); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if err := db.UpdateStepCommandSummary(context.Background(), request.TaskUUID, "probe", "ffprobe -version"); err != nil {
		t.Fatalf("UpdateStepCommandSummary: %v", err)
	}
	if err := db.RetryStep(context.Background(), request.TaskUUID, "probe", time.Now().Add(-time.Second), task.NewError(task.ErrProbeFailed, "retry", true)); err != nil {
		t.Fatalf("RetryStep: %v", err)
	}
	ready, err := db.ListReadySteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListReadySteps: %v", err)
	}
	if len(ready) != 1 || ready[0].State != string(task.StepRetryWait) {
		t.Fatalf("unexpected retry-ready step: %+v", ready)
	}
	if err := db.AddWarning(context.Background(), request.TaskUUID, WarningSpec{StepName: "probe", Code: "TEST_WARNING", Message: "test warning", Details: map[string]any{"stream": 1}}); err != nil {
		t.Fatalf("AddWarning: %v", err)
	}
	commands, err := db.ListStepCommands(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListStepCommands: %v", err)
	}
	if len(commands) != 1 || commands[0].Summary != "ffprobe -version" {
		t.Fatalf("unexpected step commands: %+v", commands)
	}
	warnings, err := db.ListWarnings(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListWarnings: %v", err)
	}
	if len(warnings) != 1 || warnings[0].Code != "TEST_WARNING" || !json.Valid([]byte(warnings[0].DetailsJSON)) {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
}

func testRequest() task.Request {
	return task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8601",
		Input:    task.Input{Type: task.InputLocal, URI: "/var/forge-worker-test/source.mov"},
		Output:   task.Output{Root: "/var/forge-worker-test/output"},
		Steps: task.StepRequests{
			Audio: task.AudioRequest{Enabled: true, Strategy: "one_per_language"},
			Video: task.VideoRequest{Enabled: true, Profiles: []string{"auto"}},
		},
	}
}
