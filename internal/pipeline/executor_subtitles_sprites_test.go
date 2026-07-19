package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func TestExecutorDoesNotMoveTaskBackwardWhenAncillaryStepStartsDuringPackaging(t *testing.T) {
	ctx := context.Background()
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := subtitleExecutorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	if err := db.SetTaskProbe(ctx, request.TaskUUID, media.Probe{
		Format: media.FormatInfo{Duration: 12},
		Subtitles: []media.SubtitleStream{{
			Index: 3, Codec: "subrip", Language: "eng",
		}},
	}); err != nil {
		t.Fatalf("SetTaskProbe: %v", err)
	}
	for _, next := range []task.State{
		task.StatePreparing, task.StateProbing, task.StateValidating, task.StateProcessing, task.StatePackaging,
	} {
		if err := db.TransitionTaskTo(ctx, request.TaskUUID, next); err != nil {
			t.Fatalf("TransitionTaskTo %s: %v", next, err)
		}
	}
	record, err := db.GetTask(ctx, request.TaskUUID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	step := findStepRecord(t, db, request.TaskUUID, StepSubtitlePackage)
	executor := NewExecutor(db, Options{CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond})

	if err := executor.runStep(ctx, request, record, step); err != nil {
		t.Fatalf("runStep: %v", err)
	}
	record, err = db.GetTask(ctx, request.TaskUUID)
	if err != nil {
		t.Fatalf("GetTask after runStep: %v", err)
	}
	if record.State != task.StatePackaging {
		t.Fatalf("task state moved backward to %s", record.State)
	}
	steps, err := db.ListSteps(ctx, request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if states := stepStateMap(steps); states[StepSubtitlePackage] != string(task.StepSucceeded) {
		t.Fatalf("subtitle step state = %s", states[StepSubtitlePackage])
	}
}

func TestExecutorExtractsTextSubtitlesAndIgnoresImageSubtitles(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := subtitleExecutorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	if err := NewExecutor(db, Options{ProbeRunner: fakeProbeRunner{stdout: subtitleProbeJSON()}, CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	states := stepStateMap(steps)
	if states[StepSubtitlePackage] != string(task.StepSucceeded) {
		t.Fatalf("subtitle_package state = %s", states[StepSubtitlePackage])
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	artifactKinds := artifactKindMap(artifacts)
	if artifactKinds["subtitle"] != "subtitles/sub_03_eng.vtt" || artifactKinds["manifest"] == "" {
		t.Fatalf("unexpected subtitle artifacts: %+v", artifacts)
	}
	var outputManifest struct {
		Subtitles []struct {
			MediaID   string `json:"media_id"`
			Path      string `json:"path"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"subtitles"`
	}
	data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &outputManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(outputManifest.Subtitles) != 1 || outputManifest.Subtitles[0].MediaID != "subtitle_03_eng" || outputManifest.Subtitles[0].Path != "subtitles/sub_03_eng.vtt" || outputManifest.Subtitles[0].SizeBytes <= 0 {
		t.Fatalf("unexpected subtitle manifest: %+v", outputManifest.Subtitles)
	}
}

func TestExecutorExtractsAllTextSubtitlesWithSingleFFmpegInput(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := subtitleExecutorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner: fakeProbeRunner{stdout: multiSubtitleProbeJSON()}, CommandRunner: runner,
		CPULimit: 4, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.ffmpegSubtitleArgs) != 1 {
		t.Fatalf("expected one subtitle ffmpeg command, got %d: %#v", len(runner.ffmpegSubtitleArgs), runner.ffmpegSubtitleArgs)
	}
	args := runner.ffmpegSubtitleArgs[0]
	joined := strings.Join(args, " ")
	for _, value := range []string{"-threads 4", "-map 0:3", "-map 0:5", "subtitles/sub_03_eng.vtt", "subtitles/sub_05_zho.vtt"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("subtitle command missing %q: %s", value, joined)
		}
	}
	if countArg(args, "-i") != 1 || strings.Contains(joined, "sub_04_jpn") {
		t.Fatalf("subtitle command should read input once and skip image subtitles: %#v", args)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	subtitlePaths := artifactPathsByKind(artifacts, "subtitle")
	if !slices.Equal(subtitlePaths, []string{"subtitles/sub_03_eng.vtt", "subtitles/sub_05_zho.vtt"}) {
		t.Fatalf("unexpected subtitle artifacts: %+v", artifacts)
	}
}

func TestExecutorGeneratesSprites(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := spriteExecutorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSON(640, 360), keyframesStdout: keyframesJSON(0, 10.2, 19.8)},
		CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	artifactKinds := artifactKindMap(artifacts)
	if artifactKinds["sprite"] != "sprites/320x180/sprite_0001.avif" || artifactKinds["manifest"] == "" {
		t.Fatalf("unexpected sprite artifacts: %+v", artifacts)
	}
	assertFileHasForgeUUIDXMP(t, filepath.Join(request.Output.Root, request.TaskUUID, "sprites", "320x180", "sprite_0001.avif"), request.TaskUUID)
	var outputManifest struct {
		Sprites []struct {
			MediaID    string    `json:"media_id"`
			FrameTimes []float64 `json:"frame_times"`
			Width      int       `json:"width"`
			Height     int       `json:"height"`
			Columns    int       `json:"columns"`
			Rows       int       `json:"rows"`
			CountFrame int       `json:"count_frame"`
			FileSize   int64     `json:"file_size"`
			Images     []string  `json:"images"`
		} `json:"sprites"`
	}
	data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &outputManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(outputManifest.Sprites) != 1 || len(outputManifest.Sprites[0].Images) != 1 || outputManifest.Sprites[0].Images[0] != "sprites/320x180/sprite_0001.avif" {
		t.Fatalf("unexpected sprite manifest images: %+v", outputManifest.Sprites)
	}
	if outputManifest.Sprites[0].MediaID != "sprite_320x180" {
		t.Fatalf("unexpected sprite media_id: %+v", outputManifest.Sprites[0])
	}
	if outputManifest.Sprites[0].Width != 320 || outputManifest.Sprites[0].Height != 180 || outputManifest.Sprites[0].Columns != 10 || outputManifest.Sprites[0].Rows != 10 {
		t.Fatalf("unexpected sprite dimensions: %+v", outputManifest.Sprites[0])
	}
	if outputManifest.Sprites[0].CountFrame != 1 || len(outputManifest.Sprites[0].FrameTimes) != 1 || outputManifest.Sprites[0].FrameTimes[0] != 10.2 {
		t.Fatalf("unexpected sprite manifest: %+v", outputManifest.Sprites)
	}
	if outputManifest.Sprites[0].FileSize <= 0 {
		t.Fatalf("sprite file_size was not recorded: %+v", outputManifest.Sprites[0])
	}
}

func TestExecutorGeneratesSpritesFromGenerated720pIntermediate(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"720p", "1080p"})
	request.Steps.Sprites = task.SpriteRequest{Enabled: true, Sizes: []string{"320x180"}, Columns: 10, Rows: 10, Quality: 70, Effort: 4, FrameFormat: "ppm"}
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner: fakeProbeRunner{stdout: probeJSONWithDuration(false, 25), keyframesStdout: keyframesJSON(0, 2, 3.2, 13.1, 23)}, CommandRunner: runner, CPULimit: 5, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.ffmpegVideoTranscodeArgs) != 2 {
		t.Fatalf("expected two serial generated video commands, got %d", len(runner.ffmpegVideoTranscodeArgs))
	}
	packageInput := filepath.Join(request.Output.Root, request.TaskUUID, "tmp", "video", "video_package.mp4")
	for index, args := range runner.ffmpegVideoTranscodeArgs {
		joinedArgs := strings.Join(args, " ")
		wantProfile := "video_1080p.mp4"
		if index == 1 {
			wantProfile = "video_720p.mp4"
		}
		for _, want := range []string{wantProfile, "expr:gte(t,n_forced*2)", "-flags +cgop", "keyint=48", "open-gop=0", "-threads 5"} {
			if !strings.Contains(joinedArgs, want) {
				t.Fatalf("generated video args missing %q: %s", want, joinedArgs)
			}
		}
		if strings.Contains(joinedArgs, "split=2") || !containsArgPair(args, "-i", packageInput) {
			t.Fatalf("profile should run independently from package input: %#v", args)
		}
		if strings.Contains(joinedArgs, "frame_%06d.") {
			t.Fatalf("generated video command should not extract sprite frames: %s", joinedArgs)
		}
	}
	if runner.ffmpegFrameExtracts != 1 || runner.vipsJoins != 1 || runner.vipsResizes != 0 {
		t.Fatalf("unexpected sprite operations: frameExtracts=%d joins=%d resizes=%d", runner.ffmpegFrameExtracts, runner.vipsJoins, runner.vipsResizes)
	}
	if len(runner.ffmpegExtractArgs) != 1 || !strings.Contains(strings.Join(runner.ffmpegExtractArgs[0], " "), filepath.Join(request.Output.Root, request.TaskUUID, "tmp", "video", "video_720p.mp4")) {
		t.Fatalf("sprites should be extracted from generated 720p intermediate: %#v", runner.ffmpegExtractArgs)
	}
	joinedExtractArgs := strings.Join(runner.ffmpegExtractArgs[0], " ")
	if !strings.Contains(joinedExtractArgs, "-threads 5") || !strings.Contains(joinedExtractArgs, "frame_%06d.ppm") {
		t.Fatalf("sprite extraction should use configured CPU limit and PPM frames: %s", joinedExtractArgs)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	states := stepStateMap(steps)
	if states[StepVideoGenerate] != string(task.StepSucceeded) || states[StepSpritesGenerate] != string(task.StepSucceeded) {
		t.Fatalf("unexpected video/sprite step states: %+v", states)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	videoIntermediates := map[string]bool{}
	var spriteArtifact state.ArtifactRecord
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "video_intermediate":
			videoIntermediates[artifact.RelativePath] = true
		case "sprite":
			spriteArtifact = artifact
		}
	}
	if !videoIntermediates["tmp/video/video_720p.mp4"] || !videoIntermediates["tmp/video/video_1080p.mp4"] || spriteArtifact.RelativePath != "sprites/320x180/sprite_0001.avif" {
		t.Fatalf("unexpected combined artifacts: %+v", artifacts)
	}
	var metadata spriteMetadata
	if err := json.Unmarshal([]byte(spriteArtifact.MetadataJSON), &metadata); err != nil {
		t.Fatalf("decode sprite metadata: %v", err)
	}
	if metadata.FrameCount != 3 || metadata.IntervalSeconds != 10 || fmt.Sprint(metadata.TimestampsSeconds) != "[3.2 13.1 23]" || metadata.Mode != "keyframe_master" {
		t.Fatalf("unexpected keyframe sprite metadata: %+v", metadata)
	}
}

func TestExecutorFallsBackToPackageSpritesWhenGeneratedIntermediateHasNoEligibleKeyframes(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"720p"})
	request.Steps.Sprites = task.SpriteRequest{Enabled: true, Sizes: []string{"320x180"}, Columns: 10, Rows: 10, Quality: 70, Effort: 4}
	ensureExecutorPlan(t, db, request)

	generated720p := filepath.Join(request.Output.Root, request.TaskUUID, "tmp", "video", "video_720p.mp4")
	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner: fakeProbeRunner{
			stdout:          probeJSONWithDuration(false, 8.6),
			keyframesStdout: keyframesJSON(0, 3.88, 6),
			keyframesByInput: map[string]string{
				generated720p: keyframesJSON(0),
			},
		},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if runner.ffmpegFrameExtracts != 1 || len(runner.ffmpegExtractArgs) != 1 {
		t.Fatalf("expected one sprite extraction, got %d args=%#v", runner.ffmpegFrameExtracts, runner.ffmpegExtractArgs)
	}
	joined := strings.Join(runner.ffmpegExtractArgs[0], " ")
	packageInput := filepath.Join(request.Output.Root, request.TaskUUID, "tmp", "video", "video_package.mp4")
	if !strings.Contains(joined, packageInput) || strings.Contains(joined, generated720p) {
		t.Fatalf("sprites should fall back to package input: %s", joined)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	var spriteArtifact state.ArtifactRecord
	for _, artifact := range artifacts {
		if artifact.Kind == "sprite" {
			spriteArtifact = artifact
			break
		}
	}
	if spriteArtifact.RelativePath != "sprites/320x180/sprite_0001.avif" {
		t.Fatalf("expected sprite artifact after source fallback: %+v", artifacts)
	}
	var metadata spriteMetadata
	if err := json.Unmarshal([]byte(spriteArtifact.MetadataJSON), &metadata); err != nil {
		t.Fatalf("decode sprite metadata: %v", err)
	}
	if metadata.FrameCount != 1 || fmt.Sprint(metadata.TimestampsSeconds) != "[3.88]" {
		t.Fatalf("unexpected fallback sprite metadata: %+v", metadata)
	}
}

func TestExecutorSpriteFailureDoesNotInvalidateGeneratedVideoArtifact(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"720p"})
	request.Steps.Sprites = task.SpriteRequest{Enabled: true, Sizes: []string{"320x180"}, Columns: 10, Rows: 10, Quality: 70, Effort: 4}
	ensureExecutorPlan(t, db, request)

	err := NewExecutor(db, Options{
		ProbeRunner: fakeProbeRunner{stdout: probeJSONWithDuration(false, 21), keyframesStdout: keyframesJSON(0, 3.2, 13.1)}, CommandRunner: &failingVipsRunner{fakeCommandRunner: fakeCommandRunner{t: t}},
		RetryInitial: time.Millisecond,
	}).Run(context.Background(), request)
	var taskErr *task.Error
	if !errors.As(err, &taskErr) || taskErr.Code != task.ErrSpriteGenerationFailed {
		t.Fatalf("expected sprite generation failure, got %v", err)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	foundVideo := false
	for _, artifact := range artifacts {
		if artifact.Kind == "video_intermediate" && artifact.RelativePath == "tmp/video/video_720p.mp4" {
			foundVideo = true
		}
		if artifact.Kind == "sprite" {
			t.Fatalf("sprite artifacts should not be committed after sprite failure: %+v", artifacts)
		}
	}
	if !foundVideo {
		t.Fatalf("generated video artifact should remain available after sprite failure: %+v", artifacts)
	}
}

func TestExecutorGeneratesSpritesFromSingleMasterPerRatio(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := spriteExecutorRequest(t, input)
	request.TaskUUID = "019f61e1-eb9d-7a90-adba-3a6f7ecc8606"
	request.Steps.Sprites.Sizes = []string{"1280x720", "640x360", "320x180"}
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSON(1920, 1080), keyframesStdout: keyframesJSON(0, 8, 18, 28)},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if runner.ffmpegFrameExtracts != 1 {
		t.Fatalf("expected one ffmpeg keyframe extraction, got %d", runner.ffmpegFrameExtracts)
	}
	if runner.vipsJoins != 1 || runner.vipsResizes != 2 {
		t.Fatalf("unexpected vips operations: joins=%d resizes=%d", runner.vipsJoins, runner.vipsResizes)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	spriteCount := 0
	for _, artifact := range artifacts {
		if artifact.Kind == "sprite" {
			spriteCount++
		}
	}
	if spriteCount != 3 {
		t.Fatalf("sprite artifacts = %d, artifacts=%+v", spriteCount, artifacts)
	}

}

func TestExecutorGeneratesSpritesInSheetBatches(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := spriteExecutorRequest(t, input)
	request.TaskUUID = "019f61e1-eb9d-7a90-adba-3a6f7ecc8607"
	request.Steps.Sprites.Columns = 10
	request.Steps.Sprites.Rows = 10
	ensureExecutorPlan(t, db, request)

	keyframes := make([]float64, 0, 206)
	for second := 0.0; second <= 2050; second += 10 {
		keyframes = append(keyframes, second)
	}
	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: probeJSONWithDuration(false, 2050), keyframesStdout: keyframesJSON(keyframes...)},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if runner.ffmpegFrameExtracts != 3 {
		t.Fatalf("expected one ffmpeg extraction per sprite sheet, got %d", runner.ffmpegFrameExtracts)
	}
	if len(runner.ffmpegExtractArgs) != 3 {
		t.Fatalf("expected recorded ffmpeg extract args, got %d", len(runner.ffmpegExtractArgs))
	}
	for index, wantSeek := range []string{"-ss 10", "-ss 1010", "-ss 2010"} {
		joined := strings.Join(runner.ffmpegExtractArgs[index], " ")
		if !strings.Contains(joined, wantSeek) {
			t.Fatalf("sheet %d args missing %q: %s", index+1, wantSeek, joined)
		}
		if !strings.Contains(joined, "select='eq(n,0)") {
			t.Fatalf("sheet %d should select relative keyframe ordinals: %s", index+1, joined)
		}
	}
	if runner.vipsJoins != 3 || runner.vipsResizes != 0 {
		t.Fatalf("unexpected vips operations: joins=%d resizes=%d", runner.vipsJoins, runner.vipsResizes)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	spriteCount := 0
	for _, artifact := range artifacts {
		if artifact.Kind == "sprite" {
			spriteCount++
		}
	}
	if spriteCount != 3 {
		t.Fatalf("sprite artifacts = %d, artifacts=%+v", spriteCount, artifacts)
	}

	var outputManifest struct {
		Sprites []struct {
			MediaID    string    `json:"media_id"`
			FrameTimes []float64 `json:"frame_times"`
			Width      int       `json:"width"`
			Height     int       `json:"height"`
			Columns    int       `json:"columns"`
			Rows       int       `json:"rows"`
			CountFrame int       `json:"count_frame"`
			FileSize   int64     `json:"file_size"`
			Images     []string  `json:"images"`
		} `json:"sprites"`
	}
	data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &outputManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(outputManifest.Sprites) != 1 || outputManifest.Sprites[0].CountFrame != 205 || len(outputManifest.Sprites[0].FrameTimes) != 205 || len(outputManifest.Sprites[0].Images) != 3 {
		t.Fatalf("unexpected batched sprite manifest: %+v", outputManifest.Sprites)
	}
	if outputManifest.Sprites[0].MediaID != "sprite_320x180" {
		t.Fatalf("unexpected batched sprite media_id: %+v", outputManifest.Sprites[0])
	}
	if outputManifest.Sprites[0].Width != 320 || outputManifest.Sprites[0].Height != 180 || outputManifest.Sprites[0].Columns != 10 || outputManifest.Sprites[0].Rows != 10 {
		t.Fatalf("unexpected batched sprite dimensions: %+v", outputManifest.Sprites[0])
	}
	if outputManifest.Sprites[0].FileSize <= 0 {
		t.Fatalf("sprite file_size was not recorded: %+v", outputManifest.Sprites[0])
	}
}

func TestExecutorFailsSpritesWhenKeyframesAreUnavailable(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := spriteExecutorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSON(640, 360), keyframesStdout: `{"frames":[]}`},
		CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request)
	var taskErr *task.Error
	if !errors.As(err, &taskErr) || taskErr.Code != task.ErrSpriteGenerationFailed || !strings.Contains(taskErr.Message, "no source keyframes") {
		t.Fatalf("expected sprite keyframe failure, got %v", err)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if states := stepStateMap(steps); states[StepSpritesGenerate] != string(task.StepFailed) || states[StepFinalize] != string(task.StepSkipped) {
		t.Fatalf("unexpected step states: %+v", states)
	}
}
