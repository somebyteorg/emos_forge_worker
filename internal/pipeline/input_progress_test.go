package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"forge_worker/internal/download"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func TestDownloadURLWritesTransferProgressToStep(t *testing.T) {
	ctx := context.Background()
	database := state.New()
	request := task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8699",
		Input:    task.Input{Type: task.InputURL, URI: "https://example.test/source.mkv"},
		Output:   task.Output{Root: t.TempDir()},
	}
	if _, err := database.EnsureTask(ctx, request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	if err := database.EnsureSteps(ctx, request.TaskUUID, []state.StepSpec{{Name: StepDownloadURL, Kind: "download", Weight: 1, MaxAttempts: 3}}); err != nil {
		t.Fatalf("EnsureSteps: %v", err)
	}
	if err := database.StartStep(ctx, request.TaskUUID, StepDownloadURL); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	executor := NewExecutor(database, Options{Downloader: progressTestDownloader{}})
	if err := executor.downloadURL(ctx, request); err == nil {
		t.Fatal("downloadURL should return the test error")
	}
	steps, err := database.ListSteps(ctx, request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	step := steps[0]
	if step.Progress != 40 || step.TransferredBytes != 40 || step.TotalBytes != 100 || step.BytesPerSecond != 10 || step.ETASeconds != 6 {
		t.Fatalf("download step = %+v", step)
	}
}

type progressTestDownloader struct{}

func (progressTestDownloader) Download(_ context.Context, request download.Request) (download.Result, error) {
	request.OnProgress(download.Progress{
		DownloadedBytes: 40, TotalBytes: 100, Percent: 40,
		BytesPerSecond: 10, EstimatedRemaining: 6 * time.Second,
	})
	return download.Result{}, errors.New("test download stopped")
}
