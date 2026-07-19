package pipeline

import (
	"context"
	"strings"
	"time"

	"forge_worker/internal/download"
	"forge_worker/internal/media"
	"forge_worker/internal/runner"
	"forge_worker/internal/state"
)

type Downloader interface {
	Download(context.Context, download.Request) (download.Result, error)
}

type Options struct {
	FFmpegPath     string
	FFprobePath    string
	PackagerPath   string
	VIPSPath       string
	AudioChannels  int
	CPULimit       int
	EncryptionMode string
	SegmentTarget  time.Duration
	SegmentMax     time.Duration
	RetryInitial   time.Duration
	RetryMax       time.Duration
	HTTPClient     download.HTTPDoer
	Downloader     Downloader
	ProbeRunner    media.CommandRunner
	CommandRunner  media.CommandRunner
	OnStepTimes    func([]StepTime)
	OnRecovery     func(RecoveryInfo)
	EnableRecovery bool
}

type StepTime struct {
	Name     string
	Duration float64
}

type RecoveryInfo struct {
	CheckpointFound bool
	RecoveredSteps  []string
	InvalidStep     string
	InvalidArtifact string
	InvalidReason   string
}

type Executor struct {
	repo                *state.DB
	opt                 Options
	checkpointSource    sourceFingerprint
	checkpointSourceSet bool
}

func NewExecutor(repo *state.DB, opt Options) *Executor {
	if opt.RetryInitial <= 0 {
		opt.RetryInitial = 2 * time.Second
	}
	if opt.RetryMax <= 0 {
		opt.RetryMax = 5 * time.Minute
	}
	if opt.FFprobePath == "" {
		opt.FFprobePath = "ffprobe"
	}
	if opt.FFmpegPath == "" {
		opt.FFmpegPath = "ffmpeg"
	}
	if opt.PackagerPath == "" {
		opt.PackagerPath = "packager"
	}
	if opt.VIPSPath == "" {
		opt.VIPSPath = "vips"
	}
	if opt.AudioChannels <= 0 {
		opt.AudioChannels = 6
	}
	if opt.CPULimit <= 0 {
		opt.CPULimit = 2
	}
	if opt.EncryptionMode == "" {
		opt.EncryptionMode = media.PackageEncryptionNone
	}
	opt.EncryptionMode = normalizedEncryptionMode(opt.EncryptionMode)
	if opt.SegmentTarget <= 0 {
		opt.SegmentTarget = 10 * time.Second
	}
	if opt.SegmentMax <= 0 {
		opt.SegmentMax = 10 * time.Second
	}
	if opt.Downloader == nil {
		opt.Downloader = download.Downloader{HTTP: opt.HTTPClient}
	}
	if opt.ProbeRunner == nil {
		opt.ProbeRunner = runner.Runner{}
	}
	if opt.CommandRunner == nil {
		opt.CommandRunner = runner.Runner{}
	}
	return &Executor{repo: repo, opt: opt}
}

func normalizedEncryptionMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return media.PackageEncryptionNone
	}
	return mode
}
