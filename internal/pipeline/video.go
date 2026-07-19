package pipeline

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

const (
	generatedVideoSegmentDuration = 10 * time.Second
	generatedVideoGOPDuration     = 2 * time.Second
)

type generatedVideoArtifact struct {
	relativePath string
	profile      media.VideoProfile
}

type generatedVideoInput struct {
	path        string
	sourceIndex int
	mode        string
}

func (e *Executor) generateVideo(ctx context.Context, request task.Request, step state.StepRecord) error {
	probe, err := e.loadProbe(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	source, ok := primaryVideoStream(probe.VideoStreams)
	if !ok {
		return task.NewError(task.ErrUnsupportedMedia, "input has no video stream", false)
	}
	if source.DolbyVision || source.DynamicRange == media.DynamicRangeDolby {
		return taskErrorWithDetails(task.ErrUnsupportedDolbyVision, "Dolby Vision video cannot be transcoded or used for direct sprite extraction", false, map[string]any{
			"source_track_index": source.Index,
			"codec":              source.Codec,
			"dynamic_range":      source.DynamicRange,
		})
	}
	profiles, err := generatedVideoProfiles(request, source)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return &stepSkippedError{Details: map[string]any{"reason": "package video covers requested generated profiles"}}
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return profiles[i].Width*profiles[i].Height > profiles[j].Width*profiles[j].Height
	})
	segmentDuration, gopSeconds := generatedVideoTiming()
	e.setStepProgress(request.TaskUUID, step.Name, 3)
	input, err := e.generatedVideoInput(ctx, request)
	if err != nil {
		return err
	}

	root := taskRoot(request)
	videoArtifacts := make([]generatedVideoArtifact, 0, len(profiles))
	for _, profile := range profiles {
		relativePath := filepath.ToSlash(filepath.Join("tmp", "video", videoOutputName(profile)))
		outputPath := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return task.NewError(task.ErrVideoTranscodeFailed, err.Error(), true)
		}
		videoArtifacts = append(videoArtifacts, generatedVideoArtifact{relativePath: relativePath, profile: profile})
	}

	if err := e.cleanupGeneratedVideoAttempt(ctx, request, step.Name, videoArtifacts); err != nil {
		return err
	}
	e.setStepProgress(request.TaskUUID, step.Name, 6)

	for index, artifact := range videoArtifacts {
		output := media.VideoGenerateOutput{
			Output: filepath.Join(root, filepath.FromSlash(artifact.relativePath)), Profile: artifact.profile, Threads: max(1, e.opt.CPULimit),
			GOPSeconds: gopSeconds, GOPFrameRate: generatedVideoGOPFrameRate(artifact.profile, source),
			ToneMap: generatedVideoNeedsToneMap(source, artifact.profile),
		}
		args, err := media.BuildVideoGenerateArgs(media.VideoGenerateSpec{
			Input: input.path, SourceIndex: input.sourceIndex, Videos: []media.VideoGenerateOutput{output},
		})
		if err != nil {
			return task.NewError(task.ErrVideoTranscodeFailed, err.Error(), false)
		}
		start, end := indexedProgressRange(6, 92, index, len(videoArtifacts))
		summary := fmt.Sprintf("video.generate %s | %s", artifact.profile.Name, commandSummary(e.opt.FFmpegPath, args))
		if err := e.runCommandWithProgressSummary(ctx, request.TaskUUID, step.Name, e.opt.FFmpegPath, args, summary, task.ErrVideoTranscodeFailed, ffmpegCommandProgress(start, end, probe.Format.Duration)); err != nil {
			return err
		}
	}
	e.setStepProgress(request.TaskUUID, step.Name, 96)
	artifactSpecs := make([]state.ArtifactSpec, 0, len(videoArtifacts))
	for _, artifact := range videoArtifacts {
		if artifact.profile.BitrateEstimated {
			_ = e.repo.AddWarning(ctx, request.TaskUUID, state.WarningSpec{
				StepName: step.Name, Code: "VIDEO_BITRATE_ESTIMATED", Message: "source video bitrate was unavailable; output bitrate was estimated",
				Details: map[string]any{"profile": artifact.profile.Name},
			})
		}
		metadata := videoIntermediateMetadata{
			SourceIndex: input.sourceIndex, Profile: artifact.profile, Mode: "transcode",
			InputMode:              input.mode,
			SegmentDurationSeconds: seconds(segmentDuration), GOPSeconds: gopSeconds,
		}
		artifactSpecs = append(artifactSpecs, state.ArtifactSpec{StepName: step.Name, Kind: "video_intermediate", RelativePath: artifact.relativePath, Committed: true, Metadata: metadata})
	}
	return e.recordArtifactSpecs(ctx, request, artifactSpecs)
}

func (e *Executor) generatedVideoInput(ctx context.Context, request task.Request) (generatedVideoInput, error) {
	artifacts, err := e.repo.ListArtifacts(ctx, request.TaskUUID)
	if err != nil {
		return generatedVideoInput{}, err
	}
	for _, artifact := range artifacts {
		if !artifact.Committed || artifact.Kind != "video_intermediate" {
			continue
		}
		metadata, err := videoIntermediateMetadataFromArtifact(artifact)
		if err != nil {
			return generatedVideoInput{}, err
		}
		if metadata.Profile.Name != "package" {
			continue
		}
		return generatedVideoInput{
			path:        filepath.Join(taskRoot(request), filepath.FromSlash(artifact.RelativePath)),
			sourceIndex: 0,
			mode:        "package",
		}, nil
	}
	return generatedVideoInput{}, task.NewError(task.ErrVideoTranscodeFailed, "package video intermediate is required before generated video transcode", false)
}

func (e *Executor) packageSourceVideo(ctx context.Context, request task.Request, step state.StepRecord) error {
	probe, err := e.loadProbe(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	source, ok := primaryVideoStream(probe.VideoStreams)
	if !ok {
		return task.NewError(task.ErrUnsupportedMedia, "input has no video stream", false)
	}
	return e.remuxSourceVideo(ctx, request, step, source, packageVideoProfile(source), probe.Format.Duration, videoProfilesInclude(request.Steps.Video.Profiles, "package"))
}

func generatedVideoTiming() (time.Duration, float64) {
	return generatedVideoSegmentDuration, seconds(generatedVideoGOPDuration)
}

func applyGeneratedVideoFrameRate(profile media.VideoProfile, source media.VideoStream) media.VideoProfile {
	if profile.Name == "720p" && source.FrameRate > 30 {
		profile.FrameRate = source.FrameRate / 2
	}
	return profile
}

func generatedVideoNeedsToneMap(source media.VideoStream, profile media.VideoProfile) bool {
	return source.DynamicRange != "" && source.DynamicRange != media.DynamicRangeSDR && profile.DynamicRange == media.DynamicRangeSDR
}

func generatedVideoGOPFrameRate(profile media.VideoProfile, source media.VideoStream) float64 {
	if profile.FrameRate > 0 {
		return profile.FrameRate
	}
	return source.FrameRate
}

func generatedVideoProfiles(request task.Request, source media.VideoStream) ([]media.VideoProfile, error) {
	requested := transcodeVideoProfiles(request.Steps.Video.Profiles)
	if len(requested) == 0 {
		return nil, nil
	}
	videoSource := media.VideoSource{
		Width: source.Width, Height: source.Height, AverageBitrate: source.AverageBitrate,
		DynamicRange: source.DynamicRange, BitDepth: source.BitDepth,
	}
	profiles, err := media.SelectVideoProfiles(videoSource, requested)
	if err != nil {
		return nil, err
	}
	if videoProfilesInclude(request.Steps.Video.Profiles, "package") {
		profiles = skipProfilesCoveredByPackageSource(profiles, source)
	}
	for i := range profiles {
		profiles[i] = applyGeneratedVideoFrameRate(profiles[i], source)
	}
	return profiles, nil
}

func skipProfilesCoveredByPackageSource(profiles []media.VideoProfile, source media.VideoStream) []media.VideoProfile {
	if source.Width <= 0 || source.Height <= 0 {
		return profiles
	}
	result := profiles[:0]
	for _, profile := range profiles {
		if packageSourceCoversGeneratedProfile(source, profile) {
			continue
		}
		result = append(result, profile)
	}
	return result
}

func packageSourceCoversGeneratedProfile(source media.VideoStream, profile media.VideoProfile) bool {
	if profile.Width <= 0 || profile.Height <= 0 {
		return false
	}
	const tolerance = 2
	return absInt(source.Width-profile.Width) <= tolerance && absInt(source.Height-profile.Height) <= tolerance
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (e *Executor) cleanupGeneratedVideoAttempt(ctx context.Context, request task.Request, stepName string, videos []generatedVideoArtifact) error {
	if err := e.deleteStepArtifacts(ctx, request, stepName); err != nil {
		return task.NewError(task.ErrVideoTranscodeFailed, err.Error(), true)
	}
	for _, video := range videos {
		if err := removeTaskRelativeFile(request, video.relativePath); err != nil {
			return task.NewError(task.ErrVideoTranscodeFailed, err.Error(), true)
		}
	}
	return nil
}

func (e *Executor) deleteStepArtifacts(ctx context.Context, request task.Request, stepName string) error {
	artifacts, err := e.repo.ListArtifacts(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if artifact.StepName != stepName {
			continue
		}
		if err := removeTaskRelativeFile(request, artifact.RelativePath); err != nil {
			return err
		}
		if err := e.repo.DeleteArtifact(ctx, request.TaskUUID, artifact.RelativePath); err != nil {
			return err
		}
	}
	return nil
}

func removeTaskRelativeFile(request task.Request, relativePath string) error {
	path, err := taskRelativePath(request, relativePath)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func removeTaskRelativeDir(request task.Request, relativePath string) error {
	path, err := taskRelativePath(request, relativePath)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func taskRelativePath(request task.Request, relativePath string) (string, error) {
	if relativePath == "" || filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("task relative path is invalid")
	}
	root := filepath.Clean(taskRoot(request))
	path := filepath.Clean(filepath.Join(root, filepath.FromSlash(relativePath)))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("task relative path escapes task root")
	}
	return path, nil
}

func (e *Executor) remuxSourceVideo(ctx context.Context, request task.Request, step state.StepRecord, source media.VideoStream, profile media.VideoProfile, duration float64, deriveSegmentTiming bool) error {
	relativePath := filepath.ToSlash(filepath.Join("tmp", "video", videoOutputName(profile)))
	outputPath := filepath.Join(taskRoot(request), filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return task.NewError(task.ErrVideoTranscodeFailed, err.Error(), true)
	}
	e.setStepProgress(request.TaskUUID, step.Name, 5)
	args, err := media.BuildVideoRemuxArgs(media.VideoRemuxSpec{Input: preparedInputPath(request), Output: outputPath, SourceIndex: source.Index, Codec: source.Codec, Threads: max(1, e.opt.CPULimit)})
	if err != nil {
		return task.NewError(task.ErrVideoTranscodeFailed, err.Error(), false)
	}
	if err := e.runCommandWithProgress(ctx, request.TaskUUID, step.Name, e.opt.FFmpegPath, args, task.ErrVideoTranscodeFailed, ffmpegCommandProgress(5, 75, duration)); err != nil {
		return err
	}
	e.setStepProgress(request.TaskUUID, step.Name, 80)
	var segmentDuration time.Duration
	var gopSeconds float64
	if deriveSegmentTiming {
		segmentDuration, gopSeconds, err = e.sourceSegmentDuration(ctx, request, source)
		if err != nil {
			return taskErrorWithDetails(task.ErrUnsupportedMedia, err.Error(), false, e.sourceSegmentDurationErrorDetails(request, source))
		}
	}
	e.setStepProgress(request.TaskUUID, step.Name, 92)
	metadata := videoIntermediateMetadata{
		SourceIndex: source.Index, Profile: profile, Mode: "remux",
		SegmentDurationSeconds: seconds(segmentDuration), GOPSeconds: gopSeconds,
	}
	return e.recordArtifact(ctx, request, state.ArtifactSpec{StepName: step.Name, Kind: "video_intermediate", RelativePath: relativePath, Committed: true, Metadata: metadata})
}

func (e *Executor) sourceSegmentDurationErrorDetails(request task.Request, source media.VideoStream) map[string]any {
	return map[string]any{
		"input":                           preparedInputPath(request),
		"source_track_index":              source.Index,
		"codec":                           source.Codec,
		"dynamic_range":                   source.DynamicRange,
		"ffprobe_path":                    e.opt.FFprobePath,
		"keyframe_probe_duration_seconds": seconds(keyframeProbeDuration(e.opt.SegmentMax)),
		"segment_target_seconds":          seconds(e.opt.SegmentTarget),
		"segment_max_seconds":             seconds(e.opt.SegmentMax),
	}
}

func (e *Executor) sourceSegmentDuration(ctx context.Context, request task.Request, source media.VideoStream) (time.Duration, float64, error) {
	fallback := e.opt.SegmentTarget
	if fallback <= 0 {
		fallback = 10 * time.Second
	}
	if e.opt.SegmentMax > 0 && fallback > e.opt.SegmentMax {
		fallback = e.opt.SegmentMax
	}
	keyframes, err := media.RunVideoKeyframesWithRunner(ctx, e.opt.ProbeRunner, e.opt.FFprobePath, preparedInputPath(request), source.Index, keyframeProbeDuration(e.opt.SegmentMax))
	if err != nil {
		return 0, 0, fmt.Errorf("probe source video keyframes for segment duration: %w", err)
	}
	gop, ok := medianGOPDuration(keyframes)
	if !ok {
		return 0, 0, fmt.Errorf("at least two source video keyframes are required to derive GOP segment duration")
	}
	duration := segmentDurationForGOP(gop, fallback, e.opt.SegmentMax)
	return duration, seconds(gop), nil
}

func keyframeProbeDuration(segmentMax time.Duration) time.Duration {
	if segmentMax <= 0 {
		segmentMax = 10 * time.Second
	}
	duration := segmentMax * 18
	if duration < time.Minute {
		return time.Minute
	}
	if duration > 3*time.Minute {
		return 3 * time.Minute
	}
	return duration
}

func medianGOPDuration(keyframes []media.Keyframe) (time.Duration, bool) {
	if len(keyframes) < 2 {
		return 0, false
	}
	intervals := make([]float64, 0, len(keyframes)-1)
	for i := 1; i < len(keyframes); i++ {
		interval := keyframes[i].Timestamp - keyframes[i-1].Timestamp
		if interval > 0.05 {
			intervals = append(intervals, interval)
		}
	}
	if len(intervals) == 0 {
		return 0, false
	}
	sort.Float64s(intervals)
	middle := len(intervals) / 2
	if len(intervals)%2 == 1 {
		return durationFromSeconds(intervals[middle]), true
	}
	return durationFromSeconds((intervals[middle-1] + intervals[middle]) / 2), true
}

func segmentDurationForGOP(gop, target, maxDuration time.Duration) time.Duration {
	if gop <= 0 || target <= 0 {
		return target
	}
	multiple := int(math.Round(float64(target) / float64(gop)))
	if multiple < 1 {
		multiple = 1
	}
	duration := time.Duration(multiple) * gop
	if maxDuration > 0 && duration > maxDuration {
		multiple = int(math.Floor(float64(maxDuration) / float64(gop)))
		if multiple < 1 {
			multiple = 1
		}
		duration = time.Duration(multiple) * gop
	}
	return duration.Round(time.Millisecond)
}

func videoOutputName(profile media.VideoProfile) string {
	name := safeFileSegment(profile.Name)
	if name == "" {
		name = "video"
	}
	return "video_" + name + ".mp4"
}

func primaryVideoStream(streams []media.VideoStream) (media.VideoStream, bool) {
	if len(streams) == 0 {
		return media.VideoStream{}, false
	}
	for _, stream := range streams {
		if stream.Default {
			return stream, true
		}
	}
	return streams[0], true
}

func transcodeVideoProfiles(profiles []string) []string {
	result := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile == "package" {
			continue
		}
		result = append(result, profile)
	}
	return result
}

func packageVideoProfile(source media.VideoStream) media.VideoProfile {
	bitrate := source.AverageBitrate
	if bitrate <= 0 {
		bitrate = 1
	}
	codec := strings.ToLower(strings.TrimSpace(source.Codec))
	if codec == "h265" {
		codec = "hevc"
	}
	if codec == "avc" {
		codec = "h264"
	}
	return media.VideoProfile{
		Name: "package", Codec: codec, EncoderProfile: source.Profile, Width: source.Width, Height: source.Height,
		AverageBitrate: bitrate, PeakBitrate: bitrate, BufferSize: bitrate * 2, PixelFormat: source.PixelFormat,
		DynamicRange: source.DynamicRange, BitrateEstimated: source.AverageBitrate <= 0,
	}
}
