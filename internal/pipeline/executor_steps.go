package pipeline

import (
	"context"
	"errors"

	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func (e *Executor) runStep(ctx context.Context, request task.Request, record state.TaskRecord, step state.StepRecord) error {
	if err := e.transitionTaskForStep(ctx, request.TaskUUID, taskStateForStep(step.Name)); err != nil {
		return err
	}
	if err := e.repo.StartStep(ctx, request.TaskUUID, step.Name); err != nil {
		return err
	}

	var err error
	switch step.Name {
	case StepPrepare:
		err = e.prepare(ctx, request)
	case StepDownloadURL:
		err = e.downloadURL(ctx, request)
	case StepProbe:
		err = e.probe(ctx, request)
	case StepValidateInput:
		err = e.validateInput(ctx, request)
	case StepAudioSelect:
		err = e.selectAudio(ctx, request, step)
	case StepAudioAAC:
		err = e.transcodeAudioAAC(ctx, request, step)
	case StepSubtitlePackage:
		err = e.extractSubtitles(ctx, request, step)
	case StepSpritesGenerate:
		err = e.generateSprites(ctx, request, step)
	case StepVideoGenerate:
		err = e.generateVideo(ctx, request, step)
	case StepVideoPackage:
		err = e.packageSourceVideo(ctx, request, step)
	case StepAudioPackage:
		err = e.packageTracks(ctx, request, step)
	case StepValidateOutput:
		err = e.validateOutput(ctx, request)
	case StepFinalize:
		err = e.finalize(ctx, request, record)
	}
	if err != nil {
		var skipped *stepSkippedError
		if errors.As(err, &skipped) {
			if err := e.repo.SkipStep(ctx, request.TaskUUID, step.Name, skipped.Details); err != nil {
				return err
			}
			return nil
		}
		return e.handleStepError(ctx, request, step, err)
	}
	if err := e.repo.CompleteStep(ctx, request.TaskUUID, step.Name); err != nil {
		return err
	}
	switch step.Name {
	case StepValidateInput:
		if err := e.repo.TransitionTaskTo(ctx, request.TaskUUID, task.StateProcessing); err != nil {
			return err
		}
	case StepFinalize:
		if err := e.repo.FinishTask(ctx, request.TaskUUID, task.StateSucceeded); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) transitionTaskForStep(ctx context.Context, taskUUID string, target task.State) error {
	record, err := e.repo.GetTask(ctx, taskUUID)
	if err != nil {
		return err
	}
	if taskStateAtOrBeyond(record.State, target) {
		return nil
	}
	if err := e.repo.TransitionTaskTo(ctx, taskUUID, target); err != nil {
		latest, latestErr := e.repo.GetTask(ctx, taskUUID)
		if latestErr == nil && taskStateAtOrBeyond(latest.State, target) {
			return nil
		}
		return err
	}
	return nil
}

func taskStateAtOrBeyond(current, target task.State) bool {
	if current == target {
		return true
	}
	currentRank, currentOK := taskStateRank(current)
	targetRank, targetOK := taskStateRank(target)
	return currentOK && targetOK && currentRank > targetRank
}

func taskStateRank(state task.State) (int, bool) {
	switch state {
	case task.StatePreparing:
		return 1, true
	case task.StateDownloading:
		return 2, true
	case task.StateProbing:
		return 3, true
	case task.StateValidating:
		return 4, true
	case task.StateProcessing:
		return 5, true
	case task.StatePackaging:
		return 6, true
	case task.StateValidatingOutput:
		return 7, true
	case task.StateFinalizing:
		return 8, true
	default:
		return 0, false
	}
}

func taskStateForStep(stepName string) task.State {
	switch stepName {
	case StepPrepare:
		return task.StatePreparing
	case StepDownloadURL:
		return task.StateDownloading
	case StepProbe:
		return task.StateProbing
	case StepValidateInput:
		return task.StateValidating
	case StepVideoPackage:
		return task.StatePackaging
	case StepAudioPackage:
		return task.StatePackaging
	case StepValidateOutput:
		return task.StateValidatingOutput
	case StepFinalize:
		return task.StateFinalizing
	default:
		return task.StateProcessing
	}
}
