package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"forge_worker/internal/config"
	"forge_worker/internal/doctor"
	"forge_worker/internal/emos"
	"forge_worker/internal/pipeline"
	"forge_worker/internal/state"
	"forge_worker/internal/task"

	"github.com/spf13/cobra"
)

var errEMOSJobCancelled = errors.New("emos job cancelled by backend")

func newWorkerCommand(ctx context.Context, version string, stdout, stderr io.Writer) *cobra.Command {
	var envFile string
	var once bool
	var emosOpt emosFlagOptions
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run the EMOS forge queue worker",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkerWithOptions(ctx, workerOptions{EnvFile: envFile, Once: once, EMOS: emosOpt}, version, stdout, stderr)
		},
	}
	cmd.Flags().StringVar(&envFile, "env-file", "", "load configuration from this env file")
	cmd.Flags().BoolVar(&once, "once", false, "process at most one job and exit")
	addEMOSFlags(cmd, &emosOpt)
	return cmd
}

type workerOptions struct {
	EnvFile string
	Once    bool
	EMOS    emosFlagOptions
}

func runWorkerWithOptions(ctx context.Context, opt workerOptions, version string, stdout, stderr io.Writer) error {
	logOut := newTimestampWriter(stdout)
	logErr := newTimestampWriter(stderr)
	cfg, err := config.Load(opt.EnvFile)
	if err != nil {
		return err
	}
	applyEMOSFlagOptions(&cfg, opt.EMOS)
	if err := prepareWorkerConfig(&cfg); err != nil {
		return err
	}
	if err := cfg.EnsureRuntimeDirs(); err != nil {
		return err
	}
	runtime.GOMAXPROCS(cfg.CPULimit)
	report := doctor.Run(ctx, cfg)
	if !report.OK {
		fmt.Fprint(logErr, report.Human())
		return errors.New("doctor checks failed")
	}
	client, err := emos.New(cfg.EMOSURL, cfg.EMOSToken, cfg.EMOSForgeWorkerID, cfg.HTTPTimeout)
	if err != nil {
		return err
	}
	if err := client.Heartbeat(ctx); err != nil {
		return fmt.Errorf("initial heartbeat failed: %w", err)
	}
	fmt.Fprintf(logOut, "initial heartbeat ok worker=%s\n", cfg.EMOSForgeWorkerID)
	apiCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go heartbeatTicker(apiCtx, cfg, client, logErr)
	fmt.Fprintf(logOut, "forge-worker worker connected to %s as worker %s\n", cfg.EMOSURL, cfg.EMOSForgeWorkerID)
	return emosJobLoop(apiCtx, cfg, client, opt.Once, logOut, logErr)
}

func prepareWorkerConfig(cfg *config.Config) error {
	if err := validateEMOSConfig(*cfg, "worker", true); err != nil {
		return err
	}
	return cfg.Validate()
}

func heartbeatTicker(ctx context.Context, cfg config.Config, client *emos.Client, stderr io.Writer) {
	interval := cfg.HeartbeatInterval
	if interval <= 0 {
		interval = time.Minute
	}
	consecutiveFailures := 0
	for {
		delay := interval + time.Duration(rand.Int63n(int64(interval/5+1)))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := client.Heartbeat(ctx); err != nil {
			consecutiveFailures++
			fmt.Fprintf(stderr, "heartbeat failed consecutive=%d: %v\n", consecutiveFailures, err)
			continue
		}
		if consecutiveFailures > 0 {
			fmt.Fprintf(stderr, "heartbeat recovered after %d failed check(s)\n", consecutiveFailures)
			consecutiveFailures = 0
		}
	}
}

func emosJobLoop(ctx context.Context, cfg config.Config, client *emos.Client, once bool, stdout, stderr io.Writer) error {
	idleDelay := cfg.PollIdleInitial
	if idleDelay <= 0 {
		idleDelay = 30 * time.Second
	}
	checkCurrent := true
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		jobType := "next"
		if checkCurrent {
			jobType = "current"
		}
		wasCurrentCheck := checkCurrent
		jobUUID, err := pollEMOSJob(ctx, client, jobType)
		if err != nil {
			fmt.Fprintf(stderr, "job poll failed: %v\n", err)
			sleepContext(ctx, growDelay(cfg.RetryInitial, cfg.PollMax))
			continue
		}
		checkCurrent = false
		if jobUUID == "" {
			if wasCurrentCheck {
				continue
			}
			if once {
				return nil
			}
			sleepContext(ctx, idleDelay)
			continue
		}
		fmt.Fprintf(stdout, "worker poll type=%s job_uuid=%s\n", jobType, jobUUID)
		if err := runEMOSJob(ctx, cfg, client, jobUUID, stdout, stderr); err != nil {
			if errors.Is(err, errEMOSJobCancelled) {
				continue
			}
			if errors.Is(err, context.Canceled) {
				return nil
			}
			fmt.Fprintf(stderr, "job %s failed locally: %v\n", jobUUID, err)
		}
		if once {
			return nil
		}
	}
}

func pollEMOSJob(ctx context.Context, client *emos.Client, kind string) (string, error) {
	jobUUID, err := client.WorkerJob(ctx, kind)
	if err != nil {
		return "", err
	}
	if jobUUID == nil {
		return "", nil
	}
	return *jobUUID, nil
}

func runEMOSJob(ctx context.Context, cfg config.Config, client *emos.Client, jobUUID string, stdout, stderr io.Writer) error {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	guard := &emosJobGuard{}

	claim, err := client.Claim(jobCtx, jobUUID)
	if err != nil {
		return err
	}
	if claim.IsSuccess {
		fmt.Fprintf(stdout, "job %s claim accepted first_claim=true\n", jobUUID)
	} else {
		fmt.Fprintf(stdout, "job %s claim accepted first_claim=false\n", jobUUID)
	}
	info, err := client.JobInfoWithFile(jobCtx, jobUUID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "job %s info status=%s job_id=%d steps=%s source=%s\n", jobUUID, info.JobStatus, info.JobID, formatEMOSJobSteps(info.JobSteps), summarizeJobSource(info))
	if info.JobStatus == "cancelled" {
		fmt.Fprintf(stderr, "job %s cancelled by backend; skipping\n", jobUUID)
		return errEMOSJobCancelled
	}
	stopMonitor := startEMOSJobStatusMonitor(jobCtx, cfg, client, jobUUID, guard, cancel, stderr)
	defer stopMonitor()

	var runErr error
	switch info.JobStatus {
	case "processing":
		runErr = processEMOSJob(jobCtx, cfg, client, jobUUID, info, guard, stdout, stderr)
	case "uploading":
		root := filepath.Join(cfg.OutputDir, jobUUID)
		stepTimes := loadCompletedStepTimes(root)
		runErr = uploadCompletedEMOSJob(jobCtx, cfg, client, jobUUID, root, stepTimes, stdout)
		if runErr != nil {
			runErr = reportEMOSJobFailed(jobCtx, client, jobUUID, fmt.Errorf("upload failed: %w", runErr), guard, stderr)
		}
	default:
		runErr = fmt.Errorf("job %s is not processable in status %q", jobUUID, info.JobStatus)
	}
	if cancelErr := guard.cancellationError(jobCtx); cancelErr != nil {
		return cancelErr
	}
	return runErr
}

type emosJobGuard struct {
	cancelled atomic.Bool
}

func (g *emosJobGuard) markCancelled() {
	if g != nil {
		g.cancelled.Store(true)
	}
}

func (g *emosJobGuard) cancellationError(ctx context.Context) error {
	if g != nil && g.cancelled.Load() {
		return errEMOSJobCancelled
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func startEMOSJobStatusMonitor(ctx context.Context, cfg config.Config, client *emos.Client, jobUUID string, guard *emosJobGuard, cancel context.CancelFunc, stderr io.Writer) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(jobInfoPollInterval(cfg))
		defer ticker.Stop()
		lastStatus := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			info, err := client.JobInfo(ctx, jobUUID)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				fmt.Fprintf(stderr, "job %s status check failed: %v\n", jobUUID, err)
				continue
			}
			if info.JobStatus != "" && info.JobStatus != lastStatus {
				fmt.Fprintf(stderr, "job %s status=%s\n", jobUUID, info.JobStatus)
				lastStatus = info.JobStatus
			}
			if info.JobStatus == "cancelled" {
				guard.markCancelled()
				fmt.Fprintf(stderr, "job %s cancelled by backend; stopping local work\n", jobUUID)
				cancel()
				return
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func jobInfoPollInterval(cfg config.Config) time.Duration {
	interval := cfg.HeartbeatInterval
	if interval <= 0 || interval > 15*time.Second {
		interval = 15 * time.Second
	}
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func processEMOSJob(ctx context.Context, cfg config.Config, client *emos.Client, jobUUID string, info emos.JobInfo, guard *emosJobGuard, stdout, stderr io.Writer) error {
	request, err := buildEMOSRequest(cfg, jobUUID, info)
	if err != nil {
		return reportEMOSJobFailed(ctx, client, jobUUID, err, guard, stderr)
	}
	if err := validateLocalInput(request); err != nil {
		return reportEMOSJobFailed(ctx, client, jobUUID, err, guard, stderr)
	}
	store := state.New()
	if _, err := store.EnsureTask(ctx, request, task.StateDiscovered); err != nil {
		return reportEMOSJobFailed(ctx, client, jobUUID, err, guard, stderr)
	}
	plan, err := pipeline.Build(request, cfg.StepRetryMax)
	if err != nil {
		return reportEMOSJobFailed(ctx, client, jobUUID, err, guard, stderr)
	}
	if err := store.EnsureSteps(ctx, request.TaskUUID, stateStepSpecs(plan)); err != nil {
		return reportEMOSJobFailed(ctx, client, jobUUID, err, guard, stderr)
	}
	fmt.Fprintf(stdout, "processing job %s -> %s\n", jobUUID, filepath.Join(request.Output.Root, request.TaskUUID))
	fmt.Fprintf(stdout, "job %s pipeline plan steps=%d video=%v audio=%t subtitles=%t sprites=%v encryption=%s\n", jobUUID, len(plan.Steps), request.Steps.Video.Profiles, request.Steps.Audio.Enabled, request.Steps.Subtitles.Enabled, request.Steps.Sprites.Sizes, cfg.EncryptionMode)
	var pipelineStepTimes []pipeline.StepTime
	err = runWorkerTaskPipelineWithStepTimes(ctx, cfg, store, request, stderr, func(stepTimes []pipeline.StepTime) {
		pipelineStepTimes = append([]pipeline.StepTime(nil), stepTimes...)
	})
	if err != nil {
		return reportEMOSJobFailed(ctx, client, jobUUID, err, guard, stderr)
	}
	stepTimes := completedStepTimesFromPipeline(pipelineStepTimes)
	fmt.Fprintf(stdout, "job %s pipeline completed\n", jobUUID)
	if err := uploadCompletedEMOSJob(ctx, cfg, client, jobUUID, filepath.Join(request.Output.Root, request.TaskUUID), stepTimes, stdout); err != nil {
		return reportEMOSJobFailed(ctx, client, jobUUID, fmt.Errorf("upload failed: %w", err), guard, stderr)
	}
	return nil
}

func reportEMOSJobFailed(ctx context.Context, client *emos.Client, jobUUID string, cause error, guard *emosJobGuard, stderr io.Writer) error {
	if cause == nil {
		return nil
	}
	if cancelErr := guard.cancellationError(ctx); cancelErr != nil {
		return cancelErr
	}
	if errors.Is(cause, errEMOSJobCancelled) || errors.Is(cause, context.Canceled) {
		return cause
	}
	fmt.Fprintf(stderr, "job %s reporting failed: %v\n", jobUUID, cause)
	if err := client.Failed(context.Background(), jobUUID, cause.Error()); err != nil {
		return fmt.Errorf("%w; failed to report job failure: %v", cause, err)
	}
	fmt.Fprintf(stderr, "job %s failed endpoint accepted\n", jobUUID)
	return cause
}

func buildEMOSRequest(cfg config.Config, jobUUID string, info emos.JobInfo) (task.Request, error) {
	input := ""
	if info.FileURL != nil {
		input = strings.TrimSpace(*info.FileURL)
	}
	if input == "" && info.FilePath != nil {
		input = strings.TrimSpace(*info.FilePath)
	}
	if input == "" {
		return task.Request{}, fmt.Errorf("job info has no file_url or file_path")
	}
	selection, err := translateEMOSJobSteps(info.JobSteps)
	if err != nil {
		return task.Request{}, err
	}
	audioEnabled := selection.AudioPackage || selection.AudioAAC
	request := task.Request{
		TaskUUID: jobUUID,
		Input:    task.Input{Type: detectInputKind(input), URI: input},
		Output:   task.Output{Root: cfg.OutputDir},
		Steps: task.StepRequests{
			Subtitles: task.SubtitleRequest{Enabled: selection.SubtitlePackage},
			Audio:     task.AudioRequest{Enabled: audioEnabled, Strategy: "one_per_language", Package: selection.AudioPackage, AAC: selection.AudioAAC},
			Video:     task.VideoRequest{Enabled: len(selection.VideoProfiles) > 0, Profiles: selection.VideoProfiles},
			Sprites: task.SpriteRequest{
				Enabled: len(selection.SpriteSizes) > 0, Sizes: selection.SpriteSizes,
				Columns: cfg.SpriteColumns, Rows: cfg.SpriteRows, Quality: cfg.SpriteAVIFQuality, Effort: cfg.SpriteAVIFEffort, FrameFormat: cfg.SpriteFrameFormat,
			},
		},
	}
	return request, request.Validate()
}

func uploadCompletedEMOSJob(ctx context.Context, cfg config.Config, client *emos.Client, jobUUID, root string, stepTimes []emos.StepTime, output io.Writer) error {
	fmt.Fprintf(output, "job %s upload root=%s\n", jobUUID, root)
	manifest, err := readManifest(root)
	if err != nil {
		return err
	}
	uploadManifest, skippedSubtitles := manifestForUpload(manifest, cfg.UploadTitledSubtitlesOnly)
	if skippedSubtitles > 0 {
		fmt.Fprintf(output, "job %s subtitle upload filter skipped=%d reason=missing_title\n", jobUUID, skippedSubtitles)
	}
	mediaIDs := emos.ManifestMediaIDs(uploadManifest)
	fmt.Fprintf(output, "job %s manifest loaded media_ids=%s\n", jobUUID, strings.Join(mediaIDs, ","))
	fmt.Fprintf(output, "job %s submitting manifest\n", jobUUID)
	if err := client.Manifest(ctx, jobUUID, uploadManifest); err != nil {
		return err
	}
	fmt.Fprintf(output, "job %s manifest endpoint accepted\n", jobUUID)
	uploader := emos.Uploader{Client: client, Output: output}
	if err := uploader.UploadManifestAssets(ctx, emos.UploadOptions{
		Root: root, JobUUID: jobUUID, Manifest: uploadManifest, Concurrency: cfg.UploadConcurrency, RetryMax: cfg.UploadRetryMax,
		ChunkSizeBytes: cfg.UploadChunkSizeBytes, DeleteArtifacts: cfg.UploadDeleteArtifacts, StepTimes: stepTimes,
	}); err != nil {
		return err
	}
	if err := pipeline.CleanupTaskTemporaryFiles(root); err != nil {
		fmt.Fprintf(output, "job %s warning: completed but temporary files could not be cleaned: %v\n", jobUUID, err)
		return nil
	}
	fmt.Fprintf(output, "job %s temporary files cleaned after completed endpoint accepted\n", jobUUID)
	return nil
}

func summarizeJobSource(info emos.JobInfo) string {
	if info.FilePath != nil && strings.TrimSpace(*info.FilePath) != "" {
		return "file_path=" + strings.TrimSpace(*info.FilePath)
	}
	if info.FileURL != nil && strings.TrimSpace(*info.FileURL) != "" {
		return "file_url=" + redactQuery(strings.TrimSpace(*info.FileURL))
	}
	return "<empty>"
}

func redactQuery(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.RawQuery == "" {
		return raw
	}
	parsed.RawQuery = "redacted"
	return parsed.String()
}

func readManifest(root string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest.json: %w", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest.json: %w", err)
	}
	return manifest, nil
}
