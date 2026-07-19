package doctor

import (
	"context"
	"time"

	"forge_worker/internal/config"
	"forge_worker/internal/runner"
)

func Run(ctx context.Context, cfg config.Config, options ...Options) Report {
	return RunWithRunner(ctx, cfg, runner.Runner{}, options...)
}

func RunWithRunner(ctx context.Context, cfg config.Config, commandRunner CommandRunner, options ...Options) Report {
	opt := doctorOptions(options)
	report := Report{OK: true, CheckedAt: time.Now().UTC().Format(time.RFC3339), Env: envSummary(cfg)}
	if err := cfg.Validate(); err != nil {
		report.add(Check{Name: "config", Status: Fail, Message: err.Error()})
	} else {
		report.add(Check{Name: "config", Status: Pass, Message: "configuration is valid"})
	}

	ffmpegPath, ok := executable(&report, "ffmpeg.executable", cfg.FFmpegPath)
	if ok {
		checkFFmpeg(ctx, commandRunner, &report, ffmpegPath, opt)
	}
	ffprobePath, ok := executable(&report, "ffprobe.executable", cfg.FFprobePath)
	if ok {
		checkVersion(ctx, commandRunner, &report, "ffprobe.version", ffprobePath, []string{"-version"}, opt)
	}
	packagerPath, ok := executable(&report, "packager.executable", cfg.PackagerPath)
	if ok {
		checkPackager(ctx, commandRunner, &report, packagerPath, opt)
	}
	vipsPath, ok := executable(&report, "vips.executable", cfg.VIPSPath)
	if ok {
		checkVIPS(ctx, commandRunner, &report, vipsPath, opt)
	}
	checkOutput(&report, cfg.OutputDir)
	return report
}

func envSummary(cfg config.Config) EnvSummary {
	return EnvSummary{
		EMOSURL:               cfg.EMOSURL,
		EMOSTokenSet:          cfg.EMOSToken != "",
		EMOSForgeWorkerID:     cfg.EMOSForgeWorkerID,
		OutputDir:             cfg.OutputDir,
		CPULimit:              cfg.CPULimit,
		HTTPTimeout:           cfg.HTTPTimeout.String(),
		HeartbeatInterval:     cfg.HeartbeatInterval.String(),
		UploadConcurrency:     cfg.UploadConcurrency,
		UploadRetryMax:        cfg.UploadRetryMax,
		UploadDeleteArtifacts: cfg.UploadDeleteArtifacts,
		FFmpegPath:            cfg.FFmpegPath,
		FFprobePath:           cfg.FFprobePath,
		PackagerPath:          cfg.PackagerPath,
		VIPSPath:              cfg.VIPSPath,
	}
}

func doctorOptions(options []Options) Options {
	if len(options) == 0 {
		return Options{}
	}
	return options[0]
}
