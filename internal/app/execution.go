package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"forge_worker/internal/config"
	"forge_worker/internal/pipeline"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func pipelineOptions(cfg config.Config) pipeline.Options {
	client := pipeline.NewHTTPClient(cfg.HTTPTimeout, cfg.DownloadConnectTimeout)
	return pipeline.Options{
		FFmpegPath:     cfg.FFmpegPath,
		FFprobePath:    cfg.FFprobePath,
		PackagerPath:   cfg.PackagerPath,
		VIPSPath:       cfg.VIPSPath,
		AudioChannels:  cfg.AudioMaxChannels,
		CPULimit:       cfg.CPULimit,
		EncryptionMode: cfg.EncryptionMode,
		SegmentTarget:  cfg.SegmentTarget,
		SegmentMax:     cfg.SegmentMax,
		RetryInitial:   cfg.RetryInitial,
		RetryMax:       cfg.RetryMax,
		HTTPClient:     client,
	}
}

func runLocalTask(ctx context.Context, cfg config.Config, database *state.DB, request task.Request, output io.Writer) error {
	localCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-localCtx.Done()
		stop()
	}()
	err := runTaskPipeline(localCtx, cfg, database, request, output)
	return err
}

func runTaskPipeline(ctx context.Context, cfg config.Config, database *state.DB, request task.Request, output io.Writer) error {
	return runTaskPipelineWithStepTimes(ctx, cfg, database, request, output, nil)
}

func runTaskPipelineWithStepTimes(ctx context.Context, cfg config.Config, database *state.DB, request task.Request, output io.Writer, onStepTimes func([]pipeline.StepTime)) error {
	return runTaskPipelineWithOptions(ctx, cfg, database, request, output, false, onStepTimes)
}

func runWorkerTaskPipelineWithStepTimes(ctx context.Context, cfg config.Config, database *state.DB, request task.Request, output io.Writer, onStepTimes func([]pipeline.StepTime)) error {
	return runTaskPipelineWithOptions(ctx, cfg, database, request, output, true, onStepTimes)
}

func runTaskPipelineWithOptions(ctx context.Context, cfg config.Config, database *state.DB, request task.Request, output io.Writer, enableRecovery bool, onStepTimes func([]pipeline.StepTime)) error {
	return runObservedTask(ctx, database, request.TaskUUID, output, func() error {
		opt := pipelineOptions(cfg)
		opt.OnStepTimes = onStepTimes
		opt.EnableRecovery = enableRecovery
		if enableRecovery {
			opt.OnRecovery = func(info pipeline.RecoveryInfo) {
				fmt.Fprintf(output, "resume checkpoint recovered_steps=%d", len(info.RecoveredSteps))
				if info.InvalidStep != "" {
					fmt.Fprintf(output, " invalid_step=%s", info.InvalidStep)
					if info.InvalidArtifact != "" {
						fmt.Fprintf(output, " invalid_artifact=%s", info.InvalidArtifact)
					}
					if info.InvalidReason != "" {
						fmt.Fprintf(output, " reason=%q", info.InvalidReason)
					}
				}
				fmt.Fprintln(output)
			}
		}
		return pipeline.NewExecutor(database, opt).Run(ctx, request)
	})
}

func runObservedTask(ctx context.Context, database *state.DB, taskUUID string, output io.Writer, execute func() error) error {
	observerDone := startTaskObserver(ctx, database, taskUUID, output)
	defer observerDone()
	return execute()
}
