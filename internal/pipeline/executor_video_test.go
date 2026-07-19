package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"forge_worker/internal/media"
	"forge_worker/internal/task"
)

func TestExecutorSkipsVideoProfilesThatRequireUpsampling(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"720p", "1080p"})
	ensureExecutorPlan(t, db, request)

	if err := NewExecutor(db, Options{ProbeRunner: fakeProbeRunner{stdout: videoProbeJSON(1280, 720), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)}, CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	states := stepStateMap(steps)
	if states[StepVideoGenerate] != string(task.StepSucceeded) {
		t.Fatalf("unexpected video step states: %+v", states)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	artifactKinds := artifactKindMap(artifacts)
	videoIntermediates := artifactPathsByKind(artifacts, "video_intermediate")
	if !slices.Contains(videoIntermediates, "tmp/video/video_package.mp4") || !slices.Contains(videoIntermediates, "tmp/video/video_720p.mp4") || artifactKinds["video_packaged"] == "" || artifactKinds["manifest"] == "" {
		t.Fatalf("unexpected video artifacts: %+v", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.RelativePath == "tmp/video/video_1080p.mp4" {
			t.Fatalf("1080p should not be generated from a 720p source: %+v", artifacts)
		}
	}
}

func TestExecutorTranscodedVideoUsesTwoSecondGOPAndTenSecondSegments(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"720p"})
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSON(1920, 1080), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)},
		CommandRunner: runner, CPULimit: 4, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.ffmpegVideoTranscodeArgs) != 1 {
		t.Fatalf("expected one video transcode command, got %d", len(runner.ffmpegVideoTranscodeArgs))
	}
	ffmpegArgs := runner.ffmpegVideoTranscodeArgs[0]
	packageInput := filepath.Join(request.Output.Root, request.TaskUUID, "tmp", "video", "video_package.mp4")
	if !containsArgPair(ffmpegArgs, "-i", packageInput) || !containsArgPair(ffmpegArgs, "-threads", "4") {
		t.Fatalf("transcode should use package input and full CPU limit: %#v", ffmpegArgs)
	}
	if !containsArgPair(ffmpegArgs, "-force_key_frames", "expr:gte(t,n_forced*2)") || !containsArgPair(ffmpegArgs, "-sc_threshold", "0") || !containsArgPair(ffmpegArgs, "-x264-params", "keyint=48:min-keyint=48:scenecut=0:open-gop=0") {
		t.Fatalf("transcode args did not force a 2 second closed GOP: %#v", ffmpegArgs)
	}
	if len(runner.packagerArgs) != 1 || !containsArgPair(runner.packagerArgs[0], "--segment_duration", "10") {
		t.Fatalf("packager args did not use 10 second segments: %#v", runner.packagerArgs)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "video_intermediate" {
			continue
		}
		metadata, err := videoIntermediateMetadataFromArtifact(artifact)
		if err != nil {
			t.Fatalf("videoIntermediateMetadataFromArtifact: %v", err)
		}
		if metadata.GOPSeconds != 2 || metadata.SegmentDurationSeconds != 10 {
			t.Fatalf("unexpected video metadata: %+v", metadata)
		}
		return
	}
	t.Fatalf("video intermediate artifact not found: %+v", artifacts)
}

func TestExecutorConvertsHDR720pToEightBitSDR(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"720p"})
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: hdrVideoProbeJSON(3840, 2160), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.ffmpegVideoTranscodeArgs) != 1 {
		t.Fatalf("expected one video transcode command, got %d", len(runner.ffmpegVideoTranscodeArgs))
	}
	joined := strings.Join(runner.ffmpegVideoTranscodeArgs[0], " ")
	for _, want := range []string{"tonemap=hable", "-c:v libx264", "-profile:v high", "-pix_fmt yuv420p"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("HDR 720p transcode args missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "high10") || strings.Contains(joined, "yuv420p10le") {
		t.Fatalf("HDR 720p should not use 10-bit profile/pix_fmt: %s", joined)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "video_intermediate" {
			continue
		}
		metadata, err := videoIntermediateMetadataFromArtifact(artifact)
		if err != nil {
			t.Fatalf("videoIntermediateMetadataFromArtifact: %v", err)
		}
		if metadata.Profile.DynamicRange != media.DynamicRangeSDR || metadata.Profile.PixelFormat != "yuv420p" || metadata.Profile.EncoderProfile != "high" {
			t.Fatalf("unexpected HDR 720p metadata: %+v", metadata.Profile)
		}
		return
	}
	t.Fatalf("video intermediate artifact not found: %+v", artifacts)
}

func TestExecutor720pHalvesHighSourceFrameRate(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"720p"})
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSONWithFrameRate(1920, 1080, "60/1"), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.ffmpegVideoTranscodeArgs) != 1 {
		t.Fatalf("expected one video transcode command, got %d", len(runner.ffmpegVideoTranscodeArgs))
	}
	if !strings.Contains(strings.Join(runner.ffmpegVideoTranscodeArgs[0], " "), "fps=30") {
		t.Fatalf("720p transcode did not halve 60fps source: %#v", runner.ffmpegVideoTranscodeArgs[0])
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "video_intermediate" {
			continue
		}
		metadata, err := videoIntermediateMetadataFromArtifact(artifact)
		if err != nil {
			t.Fatalf("videoIntermediateMetadataFromArtifact: %v", err)
		}
		if metadata.Profile.FrameRate != 30 {
			t.Fatalf("720p frame rate = %f, want 30", metadata.Profile.FrameRate)
		}
		return
	}
	t.Fatalf("video intermediate artifact not found: %+v", artifacts)
}

func TestGeneratedVideoFrameRateRules(t *testing.T) {
	tests := []struct {
		name       string
		profile    media.VideoProfile
		sourceRate float64
		wantRate   float64
	}{
		{name: "720p 50fps", profile: media.VideoProfile{Name: "720p"}, sourceRate: 50, wantRate: 25},
		{name: "720p 60fps", profile: media.VideoProfile{Name: "720p"}, sourceRate: 60, wantRate: 30},
		{name: "720p 30fps unchanged", profile: media.VideoProfile{Name: "720p"}, sourceRate: 30, wantRate: 0},
		{name: "1080p 60fps unchanged", profile: media.VideoProfile{Name: "1080p"}, sourceRate: 60, wantRate: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := applyGeneratedVideoFrameRate(tt.profile, media.VideoStream{FrameRate: tt.sourceRate})
			if profile.FrameRate != tt.wantRate {
				t.Fatalf("FrameRate = %f, want %f", profile.FrameRate, tt.wantRate)
			}
		})
	}
}

func TestGeneratedVideoToneMapRules(t *testing.T) {
	if !generatedVideoNeedsToneMap(media.VideoStream{DynamicRange: media.DynamicRangeHDR10}, media.VideoProfile{Name: "720p", DynamicRange: media.DynamicRangeSDR}) {
		t.Fatalf("HDR source to SDR profile should tone map")
	}
	if generatedVideoNeedsToneMap(media.VideoStream{DynamicRange: media.DynamicRangeHDR10}, media.VideoProfile{Name: "1080p", DynamicRange: media.DynamicRangeHDR10}) {
		t.Fatalf("HDR source to HDR profile should not tone map")
	}
	if generatedVideoNeedsToneMap(media.VideoStream{DynamicRange: media.DynamicRangeSDR}, media.VideoProfile{Name: "720p", DynamicRange: media.DynamicRangeSDR}) {
		t.Fatalf("SDR source to SDR profile should not tone map")
	}
}

func TestExecutorPackageVideoUsesSourceGOPForSegmenting(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"package"})
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSON(1920, 1080), keyframesStdout: keyframesJSON(0, 4, 8, 12, 16)},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.packagerArgs) != 1 || !containsArgPair(runner.packagerArgs[0], "--segment_duration", "8") {
		t.Fatalf("package video did not use source GOP-derived segment duration: %#v", runner.packagerArgs)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "video_intermediate" {
			continue
		}
		metadata, err := videoIntermediateMetadataFromArtifact(artifact)
		if err != nil {
			t.Fatalf("videoIntermediateMetadataFromArtifact: %v", err)
		}
		if metadata.Mode != "remux" || metadata.GOPSeconds != 4 || metadata.SegmentDurationSeconds != 8 {
			t.Fatalf("unexpected package video metadata: %+v", metadata)
		}
		return
	}
	t.Fatalf("video intermediate artifact not found: %+v", artifacts)
}

func TestExecutorPackagesPackageAndTranscodedVideoProfiles(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"package", "720p", "1080p"})
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSON(1920, 1080), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	videoIntermediates := map[string]bool{}
	videoPackaged := 0
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "video_intermediate":
			videoIntermediates[artifact.RelativePath] = true
		case "video_packaged":
			videoPackaged++
		}
	}
	if !videoIntermediates["tmp/video/video_package.mp4"] || !videoIntermediates["tmp/video/video_720p.mp4"] {
		t.Fatalf("missing package or transcoded intermediate: %+v", artifacts)
	}
	if videoIntermediates["tmp/video/video_1080p.mp4"] {
		t.Fatalf("1080p generated intermediate should be skipped when package source is already 1080p: %+v", artifacts)
	}
	if videoPackaged != 2 {
		t.Fatalf("video packaged artifacts = %d, artifacts=%+v", videoPackaged, artifacts)
	}
	generateArgs := generatedVideoCommandArgs(runner.ffmpegVideoTranscodeArgs)
	if len(generateArgs) == 0 {
		t.Fatalf("generated video command not found: %#v", runner.ffmpegVideoTranscodeArgs)
	}
	packageInput := filepath.Join(request.Output.Root, request.TaskUUID, "tmp", "video", "video_package.mp4")
	if !containsArgPair(generateArgs, "-i", packageInput) {
		t.Fatalf("generated video should read package intermediate %s, args=%#v", packageInput, generateArgs)
	}
	generatedMetadataByPath := map[string]videoIntermediateMetadata{}
	for _, artifact := range artifacts {
		if artifact.Kind != "video_intermediate" {
			continue
		}
		metadata, err := videoIntermediateMetadataFromArtifact(artifact)
		if err != nil {
			t.Fatalf("videoIntermediateMetadataFromArtifact: %v", err)
		}
		generatedMetadataByPath[artifact.RelativePath] = metadata
	}
	if generatedMetadataByPath["tmp/video/video_720p.mp4"].InputMode != "package" {
		t.Fatalf("720p input mode = %q, want package", generatedMetadataByPath["tmp/video/video_720p.mp4"].InputMode)
	}
	var outputManifest struct {
		VideoTracks []struct {
			MediaID string `json:"media_id"`
		} `json:"video_tracks"`
	}
	data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &outputManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	videoIDs := map[string]bool{}
	for _, track := range outputManifest.VideoTracks {
		videoIDs[track.MediaID] = true
	}
	if !videoIDs["video_package"] || !videoIDs["video_720p"] {
		t.Fatalf("manifest video tracks missing package or 720p media_id: %+v", outputManifest.VideoTracks)
	}
	if videoIDs["video_1080p"] {
		t.Fatalf("manifest should not include generated 1080p when package source is already 1080p: %+v", outputManifest.VideoTracks)
	}
}

func TestExecutorSkipsGeneratedVideoWhenPackageCoversOnlyRequestedProfile(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"package", "1080p"})
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSON(1920, 1080), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	states := stepStateMap(mustListSteps(t, db, request.TaskUUID))
	if states[StepVideoGenerate] != string(task.StepSkipped) || states[StepAudioPackage] != string(task.StepSucceeded) {
		t.Fatalf("unexpected step states: %+v", states)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	videoIntermediates := map[string]bool{}
	videoPackaged := 0
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "video_intermediate":
			videoIntermediates[artifact.RelativePath] = true
		case "video_packaged":
			videoPackaged++
		}
	}
	if !videoIntermediates["tmp/video/video_package.mp4"] || videoIntermediates["tmp/video/video_1080p.mp4"] {
		t.Fatalf("unexpected video intermediates: %+v", artifacts)
	}
	if videoPackaged != 1 {
		t.Fatalf("video packaged artifacts = %d, artifacts=%+v", videoPackaged, artifacts)
	}
	if generateArgs := generatedVideoCommandArgs(runner.ffmpegVideoTranscodeArgs); len(generateArgs) != 0 {
		t.Fatalf("generated video command should not run: %#v", generateArgs)
	}
}

func generatedVideoCommandArgs(commands [][]string) []string {
	for _, args := range commands {
		if slices.Contains(args, "-filter_complex") {
			return args
		}
	}
	return nil
}
