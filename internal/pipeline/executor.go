package pipeline

import (
	"context"

	"forge_worker/internal/task"
)

func (e *Executor) Run(ctx context.Context, request task.Request) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if e.opt.EnableRecovery {
		info, err := e.restoreCheckpoint(ctx, request)
		if err != nil {
			return err
		}
		if e.opt.OnRecovery != nil && info.CheckpointFound {
			e.opt.OnRecovery(info)
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := e.repo.GetTask(ctx, request.TaskUUID)
		if err != nil {
			return err
		}
		if record.State.Terminal() {
			if e.opt.OnStepTimes != nil && record.State == task.StateSucceeded {
				steps, err := e.repo.ListSteps(ctx, request.TaskUUID)
				if err != nil {
					return err
				}
				e.opt.OnStepTimes(stepTimesFromFinalizedSteps(steps))
			}
			return nil
		}
		ready, err := e.repo.ListReadySteps(ctx, request.TaskUUID)
		if err != nil {
			return err
		}
		if len(ready) == 0 {
			return nil
		}
		if err := e.runStep(ctx, request, record, ready[0]); err != nil {
			return err
		}
		if e.opt.EnableRecovery {
			if err := e.saveCheckpoint(ctx, request); err != nil {
				return err
			}
		}
	}
}
