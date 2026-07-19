package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"time"

	"forge_worker/internal/config"
	"forge_worker/internal/pipeline"
	"forge_worker/internal/state"
	"forge_worker/internal/task"

	"github.com/spf13/cobra"
)

func newLocalCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	opt := localOptions{
		VideoProfiles: "package",
		AudioRules:    "package,aac",
		AudioStrategy: "one_per_language",
		Subtitles:     true,
		Audio:         true,
		Video:         true,
		Sprites:       true,
		SpriteSizes:   "1280x720,640x360,320x180",
		Encrypt:       true,
	}
	cmd := &cobra.Command{
		Use:   "local [input]",
		Short: "Create local media segments for one file or URL",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opt.Input == "" && len(args) > 0 {
				opt.Input = args[0]
			}
			opt.EncryptSet = cmd.Flags().Changed("encrypt")
			return runLocalWithOptions(ctx, opt, stdout, stderr)
		},
	}
	cmd.Flags().StringVar(&opt.Input, "input", "", "input path or URL")
	cmd.Flags().StringVar(&opt.TaskUUID, "uuid", "", "task UUID; generated when empty")
	cmd.Flags().StringVar(&opt.Output, "output", "", "output root; task UUID directory is added automatically")
	cmd.Flags().BoolVar(&opt.Video, "video", opt.Video, "enable video processing")
	cmd.Flags().StringVar(&opt.VideoProfiles, "video-profiles", opt.VideoProfiles, "comma-separated video profiles: package,720p,1080p,2160p")
	cmd.Flags().BoolVar(&opt.Audio, "audio", opt.Audio, "enable audio processing")
	cmd.Flags().StringVar(&opt.AudioRules, "audio-rules", opt.AudioRules, "comma-separated audio rules: package,aac")
	cmd.Flags().StringVar(&opt.AudioStrategy, "audio-strategy", opt.AudioStrategy, "audio selection strategy")
	cmd.Flags().BoolVar(&opt.Sprites, "sprites", opt.Sprites, "enable thumbnail sprite generation")
	cmd.Flags().StringVar(&opt.SpriteSizes, "sprite-sizes", opt.SpriteSizes, "comma-separated sprite sizes")
	cmd.Flags().BoolVar(&opt.Subtitles, "subtitles", opt.Subtitles, "enable text subtitle extraction")
	cmd.Flags().BoolVar(&opt.Encrypt, "encrypt", opt.Encrypt, "enable ClearKey encryption for packaged audio/video")
	return cmd
}

type localOptions struct {
	Input         string
	TaskUUID      string
	Output        string
	VideoProfiles string
	AudioRules    string
	AudioStrategy string
	Subtitles     bool
	Audio         bool
	Video         bool
	Sprites       bool
	SpriteSizes   string
	Encrypt       bool
	EncryptSet    bool
}

func runLocalWithOptions(ctx context.Context, opt localOptions, stdout, stderr io.Writer) error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	if opt.Input == "" {
		opt.Encrypt = cfg.EncryptionMode == "clearkey"
		if err := promptLocal(&opt.Input, &opt.TaskUUID, &opt.VideoProfiles, &opt.AudioRules, &opt.SpriteSizes, &opt.Subtitles, &opt.Audio, &opt.Video, &opt.Sprites, &opt.Encrypt, stdout, stderr); err != nil {
			return err
		}
		opt.EncryptSet = true
	}
	logOut := newTimestampWriter(stdout)
	logErr := newTimestampWriter(stderr)
	audioEnabled, audioPackage, audioAAC, err := audioSettingsFromRules(opt.Audio, opt.AudioRules)
	if err != nil {
		return err
	}
	opt.Audio = audioEnabled
	if opt.EncryptSet && (opt.Audio || opt.Video) {
		cfg.EncryptionMode = "none"
		if opt.Encrypt {
			cfg.EncryptionMode = "clearkey"
		}
	}
	output, err := applyLocalRuntimePaths(&cfg, opt.Output)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.EnsureRuntimeDirs(); err != nil {
		return err
	}
	runtime.GOMAXPROCS(cfg.CPULimit)
	opt.Output = output
	request, err := buildLocalRequest(cfg, opt.TaskUUID, opt.Input, opt.Output, opt.VideoProfiles, opt.AudioStrategy, opt.Subtitles, opt.Audio, audioPackage, audioAAC, opt.Video, opt.Sprites, opt.SpriteSizes, cfg.SpriteFrameFormat)
	if err != nil {
		return err
	}
	if err := validateLocalInput(request); err != nil {
		return err
	}
	database := state.New()
	created, err := database.EnsureTask(ctx, request, task.StateDiscovered)
	if err != nil {
		return err
	}
	plan, err := pipeline.Build(request, cfg.StepRetryMax)
	if err != nil {
		return err
	}
	if err := database.EnsureSteps(ctx, request.TaskUUID, stateStepSpecs(plan)); err != nil {
		return err
	}
	if created {
		fmt.Fprintf(logOut, "created local task %s\n", request.TaskUUID)
	} else {
		fmt.Fprintf(logOut, "reusing existing local task %s\n", request.TaskUUID)
	}
	fmt.Fprintf(logOut, "output directory: %s\n", localTaskOutputDir(request))
	started := time.Now()
	if err := runLocalTask(ctx, cfg, database, request, logErr); err != nil {
		return err
	}
	fmt.Fprintf(logOut, "local task %s completed pipeline in %s\n", request.TaskUUID, formatLocalDuration(time.Since(started)))
	return nil
}

func formatLocalDuration(duration time.Duration) string {
	if duration < time.Second {
		return duration.Round(time.Millisecond).String()
	}
	return duration.Round(time.Second).String()
}

func localTaskOutputDir(request task.Request) string {
	return filepath.Join(request.Output.Root, request.TaskUUID)
}

func applyLocalRuntimePaths(cfg *config.Config, output string) (string, error) {
	finalOutput := output
	if finalOutput == "" {
		finalOutput = cfg.OutputDir
	}
	absoluteOutput, err := filepath.Abs(finalOutput)
	if err != nil {
		return "", fmt.Errorf("resolve output directory: %w", err)
	}
	cfg.OutputDir = absoluteOutput
	return absoluteOutput, nil
}
