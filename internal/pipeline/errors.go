package pipeline

import (
	"context"
	"errors"
	"time"

	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

type stepSkippedError struct {
	Details any
}

func (e *stepSkippedError) Error() string {
	return "pipeline step skipped"
}

func (e *Executor) handleStepError(ctx context.Context, request task.Request, step state.StepRecord, err error) error {
	if ctx.Err() != nil {
		return err
	}
	stepErr := normalizeTaskError(step.Name, err)
	currentAttempt := step.Attempt + 1
	if stepErr.Retryable && currentAttempt < step.MaxAttempts {
		retryAt := time.Now().UTC().Add(retryDelay(e.opt.RetryInitial, e.opt.RetryMax, currentAttempt))
		if retryErr := e.repo.RetryStep(ctx, request.TaskUUID, step.Name, retryAt, stepErr); retryErr != nil {
			return retryErr
		}
		if retryErr := e.repo.TransitionTaskTo(ctx, request.TaskUUID, task.StateRetryWait); retryErr != nil {
			return retryErr
		}
		return waitUntil(ctx, retryAt)
	}
	if failErr := e.repo.FailStep(ctx, request.TaskUUID, step.Name, stepErr); failErr != nil {
		return failErr
	}
	_ = e.repo.SkipUnfinishedSteps(ctx, request.TaskUUID, map[string]any{"reason": "task failed", "failed_step": step.Name})
	finalState := task.StateFailed
	if stepErr.Code == task.ErrUnsupportedDolbyVision {
		finalState = task.StateFailedUnsupportedMedia
	}
	if finishErr := e.repo.FinishTask(ctx, request.TaskUUID, finalState); finishErr != nil {
		return finishErr
	}
	return stepErr
}

func waitUntil(ctx context.Context, deadline time.Time) error {
	delay := time.Until(deadline)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizeTaskError(stepName string, err error) *task.Error {
	var taskErr *task.Error
	if errors.As(err, &taskErr) {
		if taskErr.Step == "" {
			taskErr.Step = stepName
		}
		return taskErr
	}
	return &task.Error{Code: task.ErrUnsupportedMedia, Message: err.Error(), Retryable: false, Step: stepName}
}

func taskErrorWithDetails(code task.ErrorCode, message string, retryable bool, details map[string]any) *task.Error {
	taskErr := task.NewError(code, message, retryable)
	taskErr.Details = details
	return taskErr
}

func retryDelay(initial, max time.Duration, attempt int) time.Duration {
	if initial <= 0 {
		initial = time.Second
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := initial
	for i := 1; i < attempt; i++ {
		delay *= 2
		if max > 0 && delay >= max {
			return max
		}
	}
	if max > 0 && delay > max {
		return max
	}
	return delay
}
