package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"forge_worker/internal/runner"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func assertManifestTrackIDsAbsent(t *testing.T, data []byte) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode manifest for track ID assertion: %v", err)
	}
	for _, key := range []string{"video_tracks", "audio_tracks"} {
		tracks, ok := raw[key].([]any)
		if !ok {
			continue
		}
		for index, item := range tracks {
			object, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("manifest %s[%d] is not an object: %+v", key, index, item)
			}
			if _, ok := object["id"]; ok {
				t.Fatalf("manifest %s[%d] exposes id field:\n%s", key, index, string(data))
			}
		}
	}
}

func TestExecutorRunsLocalAVPipelineToManifest(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := executorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	executor := NewExecutor(db, Options{ProbeRunner: fakeProbeRunner{stdout: probeJSON(false), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)}, CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond})
	if err := executor.Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}

	record, err := db.GetTask(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if record.State != task.StateSucceeded || record.ProbeJSON == "" {
		t.Fatalf("unexpected task record: %+v", record)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	states := stepStateMap(steps)
	for _, name := range []string{StepPrepare, StepProbe, StepValidateInput, StepAudioSelect, StepVideoPackage, StepVideoGenerate, StepAudioPackage, StepValidateOutput, StepFinalize} {
		if states[name] != string(task.StepSucceeded) {
			t.Fatalf("step %s state = %s", name, states[name])
		}
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	artifactKinds := artifactKindMap(artifacts)
	if !strings.HasPrefix(artifactKinds["audio_intermediate"], "tmp/audio/audio_01_eng") {
		t.Fatalf("unexpected audio artifacts: %+v", artifacts)
	}
	videoIntermediates := artifactPathsByKind(artifacts, "video_intermediate")
	if !slices.Contains(videoIntermediates, "tmp/video/video_package.mp4") || !slices.Contains(videoIntermediates, "tmp/video/video_1080p.mp4") {
		t.Fatalf("unexpected video artifacts: %+v", artifacts)
	}
	if packaged := artifactPathsByKind(artifacts, "video_packaged"); !slices.Equal(packaged, []string{"video/1080p/init.mp4"}) {
		t.Fatalf("internal package source should not be delivered: %+v", packaged)
	}
	for _, kind := range []string{"audio_packaged", "video_packaged", "hls_master", "manifest", "log"} {
		if artifactKinds[kind] == "" {
			t.Fatalf("missing artifact kind %s in %+v", kind, artifacts)
		}
	}
	if artifactKinds["dash_manifest"] != "" {
		t.Fatalf("did not expect DASH manifest artifact: %+v", artifacts)
	}
	if artifactKinds["video_segment"] != "video/1080p/00001.m4s" {
		t.Fatalf("unexpected video segment path: %+v", artifacts)
	}
	if artifactKinds["audio_segment"] != "audio/01_eng_aac/00001.m4s" {
		t.Fatalf("unexpected audio segment path: %+v", artifacts)
	}
	if artifactKinds["keys"] != "" {
		t.Fatalf("did not expect keys artifact by default: %+v", artifacts)
	}
	assertFileHasForgeUUIDXMP(t, filepath.Join(request.Output.Root, request.TaskUUID, "video", "1080p", "init.mp4"), request.TaskUUID)
	assertFileHasForgeUUIDXMP(t, filepath.Join(request.Output.Root, request.TaskUUID, "audio", "01_eng_aac", "init.mp4"), request.TaskUUID)
	if _, err := os.Stat(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json")); err != nil {
		t.Fatalf("manifest.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(request.Output.Root, request.TaskUUID, "log.json")); err != nil {
		t.Fatalf("log.json missing: %v", err)
	}
	var outputManifest struct {
		SchemaVersion int            `json:"schema_version"`
		Metadata      map[string]any `json:"metadata"`
		Playback      struct {
			IndependentSegments bool `json:"independent_segments"`
			Encryption          struct {
				Scheme string `json:"scheme"`
			} `json:"encryption"`
		} `json:"playback"`
		VideoTracks []struct {
			MediaID               string         `json:"media_id"`
			Profile               string         `json:"profile"`
			KeyID                 string         `json:"key_id"`
			InitPath              string         `json:"init_path"`
			InitSizeBytes         int64          `json:"init_size_bytes"`
			PlaylistPath          string         `json:"playlist_path"`
			PlaylistSizeBytes     int64          `json:"playlist_size_bytes"`
			VariantBandwidth      int64          `json:"variant_bandwidth"`
			TargetDurationSeconds int            `json:"target_duration_seconds"`
			Metadata              map[string]any `json:"metadata"`
			Segments              []struct {
				Sequence        int64   `json:"sequence"`
				URI             string  `json:"uri"`
				Path            string  `json:"path"`
				DurationSeconds float64 `json:"duration_seconds"`
				SizeBytes       int64   `json:"size_bytes"`
			} `json:"segments"`
		} `json:"video_tracks"`
		AudioTracks []struct {
			MediaID          string         `json:"media_id"`
			InitPath         string         `json:"init_path"`
			PlaylistPath     string         `json:"playlist_path"`
			RenditionGroupID string         `json:"rendition_group_id"`
			Metadata         map[string]any `json:"metadata"`
			Segments         []struct {
				URI             string  `json:"uri"`
				Path            string  `json:"path"`
				DurationSeconds float64 `json:"duration_seconds"`
			} `json:"segments"`
		} `json:"audio_tracks"`
	}
	data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &outputManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	assertManifestTrackIDsAbsent(t, data)
	if strings.Contains(string(data), "dash_manifest") || strings.Contains(string(data), "manifest.mpd") {
		t.Fatalf("manifest should not contain DASH output:\n%s", string(data))
	}
	if strings.Contains(string(data), `"video_id"`) {
		t.Fatalf("manifest should use media_id instead of video_id:\n%s", string(data))
	}
	if strings.Contains(string(data), `"artifacts"`) {
		t.Fatalf("manifest should not expose artifacts:\n%s", string(data))
	}
	if strings.Contains(string(data), `"processing"`) || strings.Contains(string(data), `"warnings"`) {
		t.Fatalf("manifest should not expose log fields:\n%s", string(data))
	}
	if outputManifest.Metadata != nil {
		t.Fatalf("manifest should not expose top-level metadata: %+v", outputManifest.Metadata)
	}
	if outputManifest.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d", outputManifest.SchemaVersion)
	}
	if !outputManifest.Playback.IndependentSegments {
		t.Fatalf("master independent_segments was not preserved")
	}
	if outputManifest.Playback.Encryption.Scheme != "none" {
		t.Fatalf("encryption scheme = %s", outputManifest.Playback.Encryption.Scheme)
	}
	if len(outputManifest.VideoTracks) != 1 || outputManifest.VideoTracks[0].KeyID != "" {
		t.Fatalf("unexpected clear video track key ID: %+v", outputManifest.VideoTracks)
	}
	if outputManifest.VideoTracks[0].MediaID != "video_1080p" || outputManifest.VideoTracks[0].Profile != "1080p" {
		t.Fatalf("unexpected video track identity: %+v", outputManifest.VideoTracks[0])
	}
	if outputManifest.VideoTracks[0].InitPath != "video/1080p/init.mp4" || outputManifest.VideoTracks[0].PlaylistPath != "video/1080p/index.m3u8" {
		t.Fatalf("unexpected video track paths: %+v", outputManifest.VideoTracks[0])
	}
	if outputManifest.VideoTracks[0].InitSizeBytes <= 0 || outputManifest.VideoTracks[0].PlaylistSizeBytes <= 0 || outputManifest.VideoTracks[0].VariantBandwidth != 1 || outputManifest.VideoTracks[0].TargetDurationSeconds != 10 {
		t.Fatalf("video HLS fields were not expanded: %+v", outputManifest.VideoTracks[0])
	}
	if len(outputManifest.VideoTracks[0].Segments) != 1 || outputManifest.VideoTracks[0].Segments[0].URI != "00001.m4s" || outputManifest.VideoTracks[0].Segments[0].Path != "video/1080p/00001.m4s" || outputManifest.VideoTracks[0].Segments[0].DurationSeconds != 10 || outputManifest.VideoTracks[0].Segments[0].SizeBytes <= 0 {
		t.Fatalf("unexpected video segments: %+v", outputManifest.VideoTracks[0].Segments)
	}
	videoFormat, ok := outputManifest.VideoTracks[0].Metadata["format"].(map[string]any)
	if !ok {
		t.Fatalf("video track metadata.format missing: %+v", outputManifest.VideoTracks[0].Metadata)
	}
	if videoFormat["filename"] != "video/1080p/index.m3u8" {
		t.Fatalf("video metadata filename should be relative track path: %+v", videoFormat)
	}
	if _, ok := outputManifest.VideoTracks[0].Metadata["streams"].([]any); !ok {
		t.Fatalf("video track metadata.streams missing: %+v", outputManifest.VideoTracks[0].Metadata)
	}
	if len(outputManifest.AudioTracks) != 1 || outputManifest.AudioTracks[0].MediaID != "audio_01_eng_aac" || outputManifest.AudioTracks[0].InitPath != "audio/01_eng_aac/init.mp4" || outputManifest.AudioTracks[0].PlaylistPath != "audio/01_eng_aac/index.m3u8" {
		t.Fatalf("unexpected audio track paths: %+v", outputManifest.AudioTracks)
	}
	if outputManifest.AudioTracks[0].RenditionGroupID != "audio" || len(outputManifest.AudioTracks[0].Segments) != 1 || outputManifest.AudioTracks[0].Segments[0].Path != "audio/01_eng_aac/00001.m4s" || outputManifest.AudioTracks[0].Segments[0].DurationSeconds != 10 {
		t.Fatalf("audio HLS fields were not expanded: %+v", outputManifest.AudioTracks[0])
	}
	audioFormat, ok := outputManifest.AudioTracks[0].Metadata["format"].(map[string]any)
	if !ok {
		t.Fatalf("audio track metadata.format missing: %+v", outputManifest.AudioTracks[0].Metadata)
	}
	if audioFormat["filename"] != "audio/01_eng_aac/index.m3u8" {
		t.Fatalf("audio metadata filename should be relative track path: %+v", audioFormat)
	}
	if _, ok := outputManifest.AudioTracks[0].Metadata["streams"].([]any); !ok {
		t.Fatalf("audio track metadata.streams missing: %+v", outputManifest.AudioTracks[0].Metadata)
	}
	if _, err := os.Stat(filepath.Join(request.Output.Root, request.TaskUUID, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("tmp should be cleaned after success, stat err=%v", err)
	}
}

func TestExecutorCanPackageWithClearKeyEncryption(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := executorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	executor := NewExecutor(db, Options{EncryptionMode: "clearkey", ProbeRunner: fakeProbeRunner{stdout: probeJSON(false), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)}, CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond})
	if err := executor.Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if artifactKindMap(artifacts)["keys"] != "" {
		t.Fatalf("did not expect keys artifact in clearkey mode: %+v", artifacts)
	}
	var outputManifest struct {
		Playback struct {
			Encryption struct {
				Scheme    string `json:"scheme"`
				KeySystem string `json:"key_system"`
			} `json:"encryption"`
		} `json:"playback"`
		VideoTracks []struct {
			MediaID string `json:"media_id"`
			KeyID   string `json:"key_id"`
			KeyHex  string `json:"key_hex"`
		} `json:"video_tracks"`
		AudioTracks []struct {
			MediaID string `json:"media_id"`
			KeyID   string `json:"key_id"`
			KeyHex  string `json:"key_hex"`
		} `json:"audio_tracks"`
	}
	data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &outputManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	assertManifestTrackIDsAbsent(t, data)
	if outputManifest.Playback.Encryption.Scheme != "cbcs" || outputManifest.Playback.Encryption.KeySystem != "clearkey" {
		t.Fatalf("unexpected encryption info: %+v", outputManifest.Playback.Encryption)
	}
	if len(outputManifest.VideoTracks) != 1 || outputManifest.VideoTracks[0].KeyID == "" || outputManifest.VideoTracks[0].KeyHex == "" {
		t.Fatalf("expected encrypted video track key metadata: %+v", outputManifest.VideoTracks)
	}
	if outputManifest.VideoTracks[0].MediaID != "video_1080p" {
		t.Fatalf("unexpected encrypted video track identity: %+v", outputManifest.VideoTracks)
	}
	if len(outputManifest.AudioTracks) != 1 || outputManifest.AudioTracks[0].KeyID == "" || outputManifest.AudioTracks[0].KeyHex == "" {
		t.Fatalf("expected encrypted audio track key metadata: %+v", outputManifest.AudioTracks)
	}
	if _, err := os.Stat(filepath.Join(request.Output.Root, request.TaskUUID, "keys.json")); !os.IsNotExist(err) {
		t.Fatalf("keys.json should not be generated, stat err=%v", err)
	}
}

func TestCommandLoggingKeepsClearKeys(t *testing.T) {
	rawKey := "4bd718af4f0c108f2866fe63e52ee803"
	inlineKey := "99d718af4f0c108f2866fe63e52ee899"
	args := []string{
		"input=/in.mp4,stream=video,output=/out/video/init.mp4",
		"--keys", "label=video_package:key_id=00112233445566778899aabbccddeeff:key=" + rawKey,
		"--hls_master_playlist_output", "/out/master.m3u8",
		"--debug_key=label=audio:key_id=ffeeddccbbaa99887766554433221100:key=" + inlineKey,
	}

	summary := commandSummary("packager", args)
	if !strings.Contains(summary, rawKey) || !strings.Contains(summary, inlineKey) || !strings.Contains(summary, ":key=") {
		t.Fatalf("command summary should keep key material: %s", summary)
	}
	longSummary := commandSummary("cmd", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"})
	if strings.Contains(longSummary, "...") || len(strings.Fields(longSummary)) != 14 {
		t.Fatalf("command summary should keep the full command without ellipsis: %s", longSummary)
	}

	details := commandFailureDetails("packager", args, runner.Result{
		ExitCode: 1,
		Stdout:   "running with key=" + rawKey,
		Stderr:   "failed args label=audio:key_id=ffeeddccbbaa99887766554433221100:key=" + inlineKey,
	}, errors.New("packager failed key="+rawKey))
	command, ok := details["command"].([]string)
	if !ok {
		t.Fatalf("command detail has unexpected type: %#v", details["command"])
	}
	joinedCommand := strings.Join(command, " ")
	if !strings.Contains(joinedCommand, rawKey) || !strings.Contains(joinedCommand, inlineKey) || !strings.Contains(joinedCommand, ":key=") {
		t.Fatalf("command detail should keep key material: %#v", command)
	}
	if detailSummary, _ := details["command_summary"].(string); !strings.Contains(detailSummary, rawKey) || !strings.Contains(detailSummary, inlineKey) || !strings.Contains(detailSummary, ":key=") {
		t.Fatalf("command summary detail should keep key material: %s", detailSummary)
	}
	for _, key := range []string{"error", "stdout_tail", "stderr_tail"} {
		value, _ := details[key].(string)
		if !strings.Contains(value, rawKey) && !strings.Contains(value, inlineKey) {
			t.Fatalf("%s should keep key material: %s", key, value)
		}
	}

	taskErr := commandTaskError(task.ErrPackagingFailed, "packager", args, runner.Result{
		ExitCode: 1,
		Stderr:   "failed args label=audio:key_id=ffeeddccbbaa99887766554433221100:key=" + inlineKey,
	}, errors.New("packager failed key="+rawKey), false)
	if !strings.Contains(taskErr.Message, inlineKey) {
		t.Fatalf("task error message should keep key material: %s", taskErr.Message)
	}
}

func TestExecutorRecordsProcessingMetrics(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := executorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	if err := NewExecutor(db, Options{
		ProbeRunner: fakeProbeRunner{stdout: probeJSON(false), keyframesStdout: keyframesJSON(0, 3, 6, 9, 12)}, CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var outputLog struct {
		ArtifactBytesTotal  int64            `json:"artifact_bytes_total"`
		ArtifactBytesByKind map[string]int64 `json:"artifact_bytes_by_kind"`
		Steps               []struct {
			Name            string   `json:"name"`
			State           string   `json:"state"`
			DurationSeconds float64  `json:"duration_seconds"`
			Commands        []string `json:"commands"`
		} `json:"steps"`
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "log.json"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), `"command_summary"`) {
		t.Fatalf("log should not expose command_summary:\n%s", string(data))
	}
	if err := json.Unmarshal(data, &outputLog); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if outputLog.ArtifactBytesTotal <= 0 || outputLog.ArtifactBytesByKind["hls_master"] <= 0 {
		t.Fatalf("unexpected processing metrics: %+v", outputLog)
	}
	if len(outputLog.Steps) == 0 {
		t.Fatalf("processing steps were not recorded")
	}
	finalizeFound := false
	for _, step := range outputLog.Steps {
		if step.Name != StepFinalize {
			continue
		}
		finalizeFound = true
		if step.State != string(task.StepSucceeded) {
			t.Fatalf("finalize step should be succeeded in log, got %+v", step)
		}
		if len(step.Commands) != 2 {
			t.Fatalf("finalize step should record both track metadata probes, got %+v", step.Commands)
		}
		joinedCommands := strings.Join(step.Commands, "\n")
		if !strings.Contains(joinedCommands, "video/1080p/index.m3u8") || !strings.Contains(joinedCommands, "audio/01_eng_aac/index.m3u8") {
			t.Fatalf("finalize commands did not include track playlists: %+v", step.Commands)
		}
	}
	if !finalizeFound {
		t.Fatalf("finalize step was not recorded in log")
	}
}

func TestExecutorRunsMediaStagesInOrder(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8612",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps: task.StepRequests{
			Subtitles: task.SubtitleRequest{Enabled: true},
			Video:     task.VideoRequest{Enabled: true, Profiles: []string{"720p"}},
			Audio:     task.AudioRequest{Enabled: true, Strategy: "one_per_language"},
			Sprites:   task.SpriteRequest{Enabled: true, Sizes: []string{"320x180"}, Columns: 10, Rows: 10, Quality: 70, Effort: 4, FrameFormat: "ppm"},
		},
	}
	ensureExecutorPlan(t, db, request)

	commandRunner := &commandOrderRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: mediaStageProbeJSON(), keyframesStdout: keyframesJSON(0, 3.2, 13.1, 23)},
		CommandRunner: commandRunner,
		CPULimit:      3, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{StepSubtitlePackage, StepVideoPackage, StepVideoGenerate, StepAudioSelect, StepSpritesGenerate}
	last := -1
	for _, step := range want {
		index := slices.Index(commandRunner.steps, step)
		if index < 0 {
			t.Fatalf("missing %s in command order %v", step, commandRunner.steps)
		}
		if index <= last {
			t.Fatalf("media stages out of order, got %v", commandRunner.steps)
		}
		last = index
	}
}

func TestExecutorRejectsDolbyVisionAtValidateInput(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := spriteExecutorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: probeJSON(true)},
		CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request)
	var taskErr *task.Error
	if !errors.As(err, &taskErr) || taskErr.Code != task.ErrUnsupportedDolbyVision || taskErr.Step != StepValidateInput {
		t.Fatalf("expected Dolby Vision validate_input failure, got %v", err)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if states := stepStateMap(steps); states[StepProbe] != string(task.StepSucceeded) || states[StepValidateInput] != string(task.StepFailed) || states[StepSpritesGenerate] != string(task.StepSkipped) || states[StepFinalize] != string(task.StepSkipped) {
		t.Fatalf("unexpected step states: %+v", states)
	}
}

func TestExecutorFailsPackageVideoWhenGOPCannotBeDerived(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"package"})
	ensureExecutorPlan(t, db, request)

	err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: videoProbeJSON(1920, 1080), keyframesStdout: keyframesJSON(0)},
		CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request)
	var taskErr *task.Error
	if !errors.As(err, &taskErr) || taskErr.Code != task.ErrUnsupportedMedia || !strings.Contains(taskErr.Message, "at least two source video keyframes") {
		t.Fatalf("expected package GOP failure, got %v", err)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if artifactKindMap(artifacts)["video_intermediate"] != "" {
		t.Fatalf("package video artifact should not be recorded: %+v", artifacts)
	}
}

func TestExecutorRejectsDolbyVisionPackageAtValidateInput(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"package"})
	ensureExecutorPlan(t, db, request)

	err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: probeJSON(true), keyframesStdout: keyframesJSON(0, 2.5, 5, 7.5, 10)},
		CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request)
	var taskErr *task.Error
	if !errors.As(err, &taskErr) || taskErr.Code != task.ErrUnsupportedDolbyVision || taskErr.Step != StepValidateInput {
		t.Fatalf("expected Dolby Vision validate_input failure, got %v", err)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	states := stepStateMap(steps)
	if states[StepProbe] != string(task.StepSucceeded) || states[StepValidateInput] != string(task.StepFailed) || states[StepVideoPackage] != string(task.StepSkipped) || states[StepFinalize] != string(task.StepSkipped) {
		t.Fatalf("unexpected package Dolby states: %+v", states)
	}
}

func TestExecutorRejectsDolbyVisionTranscodeAndSkipsRemainingSteps(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := videoExecutorRequest(t, input, []string{"auto"})
	ensureExecutorPlan(t, db, request)

	err := NewExecutor(db, Options{ProbeRunner: fakeProbeRunner{stdout: probeJSON(true)}, RetryInitial: time.Millisecond}).Run(context.Background(), request)
	var taskErr *task.Error
	if !errors.As(err, &taskErr) || taskErr.Code != task.ErrUnsupportedDolbyVision {
		t.Fatalf("expected unsupported Dolby Vision error, got %v", err)
	}
	record, err := db.GetTask(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if record.State != task.StateFailedUnsupportedMedia {
		t.Fatalf("unexpected failed task record: %+v", record)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	states := stepStateMap(steps)
	if states[StepProbe] != string(task.StepSucceeded) {
		t.Fatalf("probe step state = %s", states[StepProbe])
	}
	if states[StepValidateInput] != string(task.StepFailed) || states[StepVideoGenerate] != string(task.StepSkipped) || states[StepAudioPackage] != string(task.StepSkipped) {
		t.Fatalf("expected downstream steps skipped, got %+v", states)
	}
}

func TestExecutorStopsBeforeStartingStepWhenContextCanceled(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mov")
	writeTestFile(t, input, []byte("media"))
	request := executorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewExecutor(db, Options{RetryInitial: time.Millisecond}).Run(ctx, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	steps, err := db.ListSteps(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	states := stepStateMap(steps)
	if states[StepPrepare] != string(task.StepPending) {
		t.Fatalf("prepare state = %s, want pending", states[StepPrepare])
	}
}

func TestExecutorRetriesRetryableStep(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := task.Request{
		TaskUUID: "019f6200-0000-7000-8000-000000000777",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps:    task.StepRequests{Subtitles: task.SubtitleRequest{Enabled: true}},
	}
	ensureExecutorPlan(t, db, request)
	probeRunner := &flakyProbeRunner{success: fakeProbeRunner{stdout: probeJSON(false)}}

	err := NewExecutor(db, Options{
		ProbeRunner: probeRunner, RetryInitial: time.Millisecond, RetryMax: time.Millisecond,
	}).Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if probeRunner.calls != 2 {
		t.Fatalf("probe attempts = %d, want 2", probeRunner.calls)
	}
	probe := findStepRecord(t, db, request.TaskUUID, StepProbe)
	if probe.State != string(task.StepSucceeded) || probe.Attempt != 2 {
		t.Fatalf("probe step = %+v, want succeeded on attempt 2", probe)
	}
}

func TestSetHLSDefaultAudioMarksSelectedPlaylist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.m3u8")
	input := `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,URI="audio/01_und_aac/index.m3u8",GROUP-ID="audio",NAME="und",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2"
#EXT-X-MEDIA:TYPE=AUDIO,URI="audio/02_eng_aac/index.m3u8",GROUP-ID="audio",NAME="eng",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2"
#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO="audio"
video/package/index.m3u8
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := setHLSDefaultAudio(path, "audio/01_und_aac/index.m3u8"); err != nil {
		t.Fatalf("setHLSDefaultAudio: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	output := string(data)
	if !strings.Contains(output, `URI="audio/01_und_aac/index.m3u8",GROUP-ID="audio",NAME="und",DEFAULT=YES`) {
		t.Fatalf("selected audio was not marked default:\n%s", output)
	}
	if !strings.Contains(output, `URI="audio/02_eng_aac/index.m3u8",GROUP-ID="audio",NAME="eng",DEFAULT=NO`) {
		t.Fatalf("previous default was not cleared:\n%s", output)
	}
}

type fakeProbeRunner struct {
	stdout           string
	keyframesStdout  string
	keyframesByInput map[string]string
	metadataStdout   string
}

type flakyProbeRunner struct {
	calls   int
	success fakeProbeRunner
}

func (r *flakyProbeRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	r.calls++
	if r.calls == 1 {
		return runner.Result{ExitCode: 1, Stderr: "temporary probe failure"}, errors.New("temporary probe failure")
	}
	return r.success.Run(ctx, spec)
}

func (r fakeProbeRunner) Run(_ context.Context, spec runner.Spec) (runner.Result, error) {
	stdout := r.stdout
	if isMetadataProbeCommand(spec.Args) {
		stdout = r.metadataStdout
		if stdout == "" {
			stdout = metadataProbeJSON()
		}
	} else if r.keyframesStdout != "" && strings.Contains(strings.Join(spec.Args, " "), "frame=stream_index") {
		stdout = r.keyframesStdout
		if len(spec.Args) > 0 && r.keyframesByInput != nil {
			if inputStdout, ok := r.keyframesByInput[spec.Args[len(spec.Args)-1]]; ok {
				stdout = inputStdout
			}
		}
	}
	return runner.Result{ExitCode: 0, Stdout: stdout, Started: time.Now().UTC(), Finished: time.Now().UTC()}, nil
}

func isMetadataProbeCommand(args []string) bool {
	return len(args) >= 5 && args[0] == "-show_format" && args[1] == "-show_streams" && args[2] == "-print_format" && args[3] == "json" && strings.HasSuffix(args[4], ".m3u8")
}

type fakeCommandRunner struct {
	t     *testing.T
	calls int
}

func (r *fakeCommandRunner) Run(_ context.Context, spec runner.Spec) (runner.Result, error) {
	if len(spec.Args) == 0 {
		r.t.Fatalf("command args are empty")
	}
	if spec.Name == "packager" {
		writeFakePackagerOutputs(r.t, spec.Args)
	} else if spec.Name == "vips" {
		output := strings.Split(spec.Args[2], "[")[0]
		writeFakeOutput(r.t, output)
	} else if spec.Name == "ffmpeg" {
		writeFakeFFmpegOutputs(r.t, spec.Args)
	} else if strings.Contains(strings.Join(spec.Args, " "), "frame_%06d.") {
		writeFakeFrameOutputs(r.t, spec.Args, "%06d", 6)
	} else if strings.Contains(strings.Join(spec.Args, " "), "frame_%04d.") {
		output := spec.Args[len(spec.Args)-1]
		writeFakeOutput(r.t, strings.Replace(output, "%04d", "0001", 1))
	} else {
		output := spec.Args[len(spec.Args)-1]
		writeFakeOutput(r.t, output)
	}
	return runner.Result{ExitCode: 0, Started: time.Now().UTC(), Finished: time.Now().UTC()}, nil
}

type commandOrderRunner struct {
	fakeCommandRunner
	steps []string
}

func (r *commandOrderRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	if spec.Name == "ffmpeg" {
		joined := strings.Join(spec.Args, " ")
		switch {
		case strings.Contains(joined, "frame_%06d."):
			r.steps = append(r.steps, StepSpritesGenerate)
		case strings.Contains(joined, "subtitles/sub_"):
			r.steps = append(r.steps, StepSubtitlePackage)
		case strings.Contains(joined, "-filter_complex") && strings.Contains(joined, "tmp/video/video_"):
			r.steps = append(r.steps, StepVideoGenerate)
		case strings.Contains(joined, "tmp/video/video_package.mp4"):
			r.steps = append(r.steps, StepVideoPackage)
		case strings.Contains(joined, "tmp/audio/audio_"):
			r.steps = append(r.steps, StepAudioSelect)
		}
	}
	return r.fakeCommandRunner.Run(ctx, spec)
}

type failingVipsRunner struct {
	fakeCommandRunner
}

func (r *failingVipsRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	if spec.Name != "vips" {
		return r.fakeCommandRunner.Run(ctx, spec)
	}
	result := runner.Result{ExitCode: 1, Stderr: "vips failed", Started: time.Now().UTC(), Finished: time.Now().UTC()}
	return result, errors.New("vips failed")
}

type recordingCommandRunner struct {
	fakeCommandRunner
	ffmpegCommands           int
	ffmpegFrameExtracts      int
	ffmpegAudioArgs          [][]string
	ffmpegSubtitleArgs       [][]string
	ffmpegExtractArgs        [][]string
	ffmpegVideoTranscodeArgs [][]string
	packagerArgs             [][]string
	vipsJoins                int
	vipsResizes              int
}

func (r *recordingCommandRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	joined := strings.Join(spec.Args, " ")
	if spec.Name == "ffmpeg" {
		r.ffmpegCommands++
	}
	if spec.Name == "ffmpeg" && strings.Contains(joined, "tmp/audio/audio_") {
		r.ffmpegAudioArgs = append(r.ffmpegAudioArgs, append([]string(nil), spec.Args...))
	}
	if spec.Name == "ffmpeg" && strings.Contains(joined, "subtitles/sub_") {
		r.ffmpegSubtitleArgs = append(r.ffmpegSubtitleArgs, append([]string(nil), spec.Args...))
	}
	if strings.Contains(joined, "frame_%06d.") {
		r.ffmpegFrameExtracts++
		r.ffmpegExtractArgs = append(r.ffmpegExtractArgs, append([]string(nil), spec.Args...))
	}
	if spec.Name == "ffmpeg" && strings.Contains(joined, "-filter_complex") && strings.Contains(joined, "video_") && strings.Contains(joined, ".mp4") && !strings.Contains(joined, "frame_") {
		r.ffmpegVideoTranscodeArgs = append(r.ffmpegVideoTranscodeArgs, append([]string(nil), spec.Args...))
	}
	if spec.Name == "packager" {
		r.packagerArgs = append(r.packagerArgs, append([]string(nil), spec.Args...))
	}
	if spec.Name == "vips" && len(spec.Args) > 0 {
		switch spec.Args[0] {
		case "arrayjoin":
			r.vipsJoins++
		case "resize":
			r.vipsResizes++
		}
	}
	return r.fakeCommandRunner.Run(ctx, spec)
}

func writeFakeFFmpegOutputs(t *testing.T, args []string) {
	t.Helper()
	wrote := false
	for i, arg := range args {
		switch {
		case strings.Contains(arg, "frame_%06d."):
			writeFakeFramePatternOutputs(t, args, i, "%06d", 6)
			wrote = true
		case strings.Contains(arg, "frame_%04d."):
			writeFakeFramePatternOutputs(t, args, i, "%04d", 4)
			wrote = true
		case isFFmpegOutputArg(args, i):
			writeFakeOutput(t, arg)
			wrote = true
		}
	}
	if !wrote {
		writeFakeOutput(t, args[len(args)-1])
	}
}

func writeFakeFramePatternOutputs(t *testing.T, args []string, outputIndex int, placeholder string, width int) {
	t.Helper()
	count := 1
	for i := outputIndex - 1; i >= 1; i-- {
		if args[i-1] != "-frames:v" {
			continue
		}
		parsed, err := strconv.Atoi(args[i])
		if err != nil {
			t.Fatalf("parse fake frame count: %v", err)
		}
		count = parsed
		break
	}
	output := args[outputIndex]
	for i := 1; i <= count; i++ {
		writeFakeOutput(t, strings.Replace(output, placeholder, fmt.Sprintf("%0*d", width, i), 1))
	}
}

func isFFmpegOutputArg(args []string, index int) bool {
	if index > 0 && args[index-1] == "-i" {
		return false
	}
	arg := args[index]
	if strings.HasPrefix(arg, "-") {
		return false
	}
	switch strings.ToLower(filepath.Ext(arg)) {
	case ".mp4", ".m4a", ".vtt":
		return true
	default:
		return false
	}
}

func writeFakeFrameOutputs(t *testing.T, args []string, placeholder string, width int) {
	t.Helper()
	count := 1
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "-frames:v" {
			continue
		}
		parsed, err := strconv.Atoi(args[i+1])
		if err != nil {
			t.Fatalf("parse fake frame count: %v", err)
		}
		count = parsed
		break
	}
	output := args[len(args)-1]
	for i := 1; i <= count; i++ {
		writeFakeOutput(t, strings.Replace(output, placeholder, fmt.Sprintf("%0*d", width, i), 1))
	}
}

func writeFakePackagerOutputs(t *testing.T, args []string) {
	t.Helper()
	encrypted := false
	for _, arg := range args {
		if arg == "--enable_raw_key_encryption" {
			encrypted = true
			break
		}
	}
	tracks := make([]fakePackagerTrack, 0)
	for _, arg := range args {
		if strings.HasPrefix(arg, "in=") {
			track := fakePackagerTrack{}
			parts := strings.Split(arg, ",")
			for _, part := range parts {
				switch {
				case strings.HasPrefix(part, "stream="):
					track.stream = strings.TrimPrefix(part, "stream=")
				case strings.HasPrefix(part, "init_segment="):
					track.initSegment = strings.TrimPrefix(part, "init_segment=")
					writeFakeOutput(t, track.initSegment)
				case strings.HasPrefix(part, "playlist_name="):
					track.playlist = strings.TrimPrefix(part, "playlist_name=")
				case strings.HasPrefix(part, "segment_template="):
					track.segment = fakeSegmentPath(strings.TrimPrefix(part, "segment_template="))
					writeFakeOutput(t, track.segment)
				case strings.HasPrefix(part, "hls_group_id="):
					track.hlsGroupID = strings.TrimPrefix(part, "hls_group_id=")
				case strings.HasPrefix(part, "hls_name="):
					track.hlsName = strings.TrimPrefix(part, "hls_name=")
				case strings.HasPrefix(part, "language="):
					track.language = strings.TrimPrefix(part, "language=")
				}
			}
			if track.playlist != "" {
				writeFakePlaylistOutput(t, track, encrypted)
				tracks = append(tracks, track)
			}
		}
	}
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--hls_master_playlist_output":
			writeFakeMasterPlaylistOutput(t, args[i+1], tracks)
		}
	}
}

type fakePackagerTrack struct {
	stream      string
	initSegment string
	playlist    string
	segment     string
	hlsGroupID  string
	hlsName     string
	language    string
}

func writeFakePlaylistOutput(t *testing.T, track fakePackagerTrack, encrypted bool) {
	t.Helper()
	output := track.playlist
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatalf("create fake playlist dir: %v", err)
	}
	var builder strings.Builder
	builder.WriteString(`#EXTM3U
#EXT-X-VERSION:6
`)
	builder.WriteString("#EXT-X-TARGETDURATION:10\n")
	builder.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	builder.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=%q\n", relativePlaylistURI(t, output, track.initSegment)))
	if encrypted {
		builder.WriteString(`#EXT-X-KEY:METHOD=SAMPLE-AES,URI="data:text/plain;base64,keyid",IV=0x00112233445566778899aabbccddeeff,KEYFORMAT="identity"` + "\n")
	}
	builder.WriteString(fmt.Sprintf(`#EXTINF:10.000,
%s
#EXT-X-ENDLIST
`, relativePlaylistURI(t, output, track.segment)))
	if err := os.WriteFile(output, []byte(builder.String()), 0o600); err != nil {
		t.Fatalf("write fake playlist: %v", err)
	}
}

func writeFakeMasterPlaylistOutput(t *testing.T, output string, tracks []fakePackagerTrack) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatalf("create fake master playlist dir: %v", err)
	}
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n")
	builder.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	hasAudio := false
	for _, track := range tracks {
		if track.stream == "audio" {
			hasAudio = true
			groupID := firstNonEmpty(track.hlsGroupID, "audio")
			name := firstNonEmpty(track.hlsName, track.language, "audio")
			builder.WriteString(fmt.Sprintf("#EXT-X-MEDIA:TYPE=AUDIO,URI=%q,GROUP-ID=%q,NAME=%q,DEFAULT=YES,AUTOSELECT=YES,CHANNELS=\"2\"\n", relativePlaylistURI(t, output, track.playlist), groupID, name))
		}
	}
	for _, track := range tracks {
		if track.stream != "video" {
			continue
		}
		audio := ""
		if hasAudio {
			audio = `,AUDIO="audio"`
		}
		builder.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=1,AVERAGE-BANDWIDTH=1,CODECS=\"avc1.64001f,mp4a.40.2\",RESOLUTION=1920x1080,FRAME-RATE=25.000%s,CLOSED-CAPTIONS=NONE\n", audio))
		builder.WriteString(relativePlaylistURI(t, output, track.playlist) + "\n")
	}
	if err := os.WriteFile(output, []byte(builder.String()), 0o600); err != nil {
		t.Fatalf("write fake master playlist: %v", err)
	}
}

func relativePlaylistURI(t *testing.T, playlistPath, targetPath string) string {
	t.Helper()
	relative, err := filepath.Rel(filepath.Dir(playlistPath), targetPath)
	if err != nil {
		t.Fatalf("relative playlist URI: %v", err)
	}
	return filepath.ToSlash(relative)
}

func fakeSegmentPath(template string) string {
	if strings.Contains(template, "$Number%05d$") {
		return strings.Replace(template, "$Number%05d$", "00001", 1)
	}
	return strings.Replace(template, "$Number$", "000001", 1)
}

func writeFakeOutput(t *testing.T, output string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatalf("create fake output dir: %v", err)
	}
	if err := os.WriteFile(output, []byte("artifact"), 0o600); err != nil {
		t.Fatalf("write fake output: %v", err)
	}
}

func openExecutorDB(t *testing.T) *state.DB {
	t.Helper()
	return state.New()
}

func executorRequest(t *testing.T, input string) task.Request {
	t.Helper()
	return task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8601",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps: task.StepRequests{
			Audio: task.AudioRequest{Enabled: true, Strategy: "one_per_language"},
			Video: task.VideoRequest{Enabled: true, Profiles: []string{"auto"}},
		},
	}
}

func packageExecutorRequest(t *testing.T, input string) task.Request {
	t.Helper()
	return task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8605",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps: task.StepRequests{
			Audio: task.AudioRequest{Enabled: true, Strategy: "one_per_language", Package: true},
			Video: task.VideoRequest{Enabled: true, Profiles: []string{"package"}},
		},
	}
}

func subtitleExecutorRequest(t *testing.T, input string) task.Request {
	t.Helper()
	return task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8602",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps: task.StepRequests{
			Subtitles: task.SubtitleRequest{Enabled: true},
		},
	}
}

func videoExecutorRequest(t *testing.T, input string, profiles []string) task.Request {
	t.Helper()
	return task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8603",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps: task.StepRequests{
			Video: task.VideoRequest{Enabled: true, Profiles: profiles},
		},
	}
}

func spriteExecutorRequest(t *testing.T, input string) task.Request {
	t.Helper()
	return task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8604",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps: task.StepRequests{
			Sprites: task.SpriteRequest{Enabled: true, Sizes: []string{"320x180"}, Columns: 10, Rows: 10, Quality: 70, Effort: 4},
		},
	}
}

func ensureExecutorPlan(t *testing.T, db *state.DB, request task.Request) {
	t.Helper()
	if _, err := db.EnsureTask(context.Background(), request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	plan, err := Build(request, 3)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	specs := make([]state.StepSpec, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		specs = append(specs, state.StepSpec{Name: step.Name, Kind: step.Kind, Weight: step.Weight, MaxAttempts: step.MaxAttempts, Dependencies: step.Dependencies})
	}
	if err := db.EnsureSteps(context.Background(), request.TaskUUID, specs); err != nil {
		t.Fatalf("EnsureSteps: %v", err)
	}
}

func stepStateMap(steps []state.StepRecord) map[string]string {
	result := make(map[string]string, len(steps))
	for _, step := range steps {
		result[step.Name] = step.State
	}
	return result
}

func mustListSteps(t *testing.T, db *state.DB, taskUUID string) []state.StepRecord {
	t.Helper()
	steps, err := db.ListSteps(context.Background(), taskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	return steps
}

func findStepRecord(t *testing.T, db *state.DB, taskUUID, name string) state.StepRecord {
	t.Helper()
	steps, err := db.ListSteps(context.Background(), taskUUID)
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	for _, step := range steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("missing step %s", name)
	return state.StepRecord{}
}

func containsArgPair(args []string, key, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func countArg(args []string, value string) int {
	count := 0
	for _, arg := range args {
		if arg == value {
			count++
		}
	}
	return count
}

func assertFileHasForgeUUIDXMP(t *testing.T, path, uuid string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read forge_uuid output %s: %v", path, err)
	}
	if !bytes.Contains(data, []byte("forge:forge_uuid=\""+uuid+"\"")) {
		t.Fatalf("forge_uuid XMP not found in %s", path)
	}
}

func artifactKindMap(artifacts []state.ArtifactRecord) map[string]string {
	result := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		result[artifact.Kind] = artifact.RelativePath
	}
	return result
}

func artifactPathsByKind(artifacts []state.ArtifactRecord, kind string) []string {
	var result []string
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			result = append(result, artifact.RelativePath)
		}
	}
	sort.Strings(result)
	return result
}

func findArtifact(t *testing.T, artifacts []state.ArtifactRecord, kind string) state.ArtifactRecord {
	t.Helper()
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			return artifact
		}
	}
	t.Fatalf("missing artifact kind %s in %+v", kind, artifacts)
	return state.ArtifactRecord{}
}

func probeJSON(dolby bool) string {
	return probeJSONWithDuration(dolby, 12)
}

func metadataProbeJSON() string {
	return `{
  "streams": [
    {"index":0,"codec_type":"audio","codec_name":"aac","profile":"LC"},
    {"index":1,"codec_type":"video","codec_name":"hevc","profile":"Main 10","width":3840,"height":2160}
  ],
  "format":{"filename":"master.m3u8","format_name":"hls","duration":"12.0","probe_score":100}
}`
}

func probeJSONWithDuration(dolby bool, duration float64) string {
	sideData := ""
	if dolby {
		sideData = `,"side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8}]`
	}
	return fmt.Sprintf(`{
  "format":{"format_name":"mov,mp4","duration":"%.1f","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,"pix_fmt":"yuv420p10le","avg_frame_rate":"24000/1001"`+sideData+`},
    {"index":1,"codec_type":"audio","codec_name":"aac","sample_rate":"48000","channels":2,"channel_layout":"stereo","tags":{"language":"eng"}}
  ]
}`, duration)
}

func subtitleProbeJSON() string {
	return `{
  "format":{"format_name":"matroska","duration":"12.0","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":3,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}},
    {"index":4,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle","tags":{"language":"jpn"}}
  ]
}`
}

func multiSubtitleProbeJSON() string {
	return `{
  "format":{"format_name":"matroska","duration":"12.0","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":3,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}},
    {"index":4,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle","tags":{"language":"jpn"}},
    {"index":5,"codec_type":"subtitle","codec_name":"ass","tags":{"language":"zho"}}
  ]
}`
}

func sourceAudioProbeJSON() string {
	return `{
  "format":{"format_name":"matroska","duration":"12.0","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,"pix_fmt":"yuv420p10le","avg_frame_rate":"24000/1001"},
    {"index":1,"codec_type":"audio","codec_name":"eac3","sample_rate":"48000","channels":6,"channel_layout":"5.1","bit_rate":"640000","tags":{"language":"eng"}}
  ]
}`
}

func multiAudioProbeJSON() string {
	return `{
  "format":{"format_name":"matroska","duration":"12.0","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":1,"codec_type":"audio","codec_name":"eac3","sample_rate":"48000","channels":6,"channel_layout":"5.1","bit_rate":"640000","tags":{"language":"eng"}},
    {"index":2,"codec_type":"audio","codec_name":"aac","profile":"LC","sample_rate":"48000","channels":2,"channel_layout":"stereo","bit_rate":"128000","tags":{"language":"jpn"}}
  ]
}`
}

func multiNonAACAudioProbeJSON() string {
	return `{
  "format":{"format_name":"matroska","duration":"12.0","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,"pix_fmt":"yuv420p10le","avg_frame_rate":"24000/1001"},
    {"index":1,"codec_type":"audio","codec_name":"eac3","sample_rate":"48000","channels":6,"channel_layout":"5.1","bit_rate":"640000","tags":{"language":"eng"}},
    {"index":2,"codec_type":"audio","codec_name":"ac3","sample_rate":"48000","channels":2,"channel_layout":"stereo","bit_rate":"192000","tags":{"language":"jpn"}}
  ]
}`
}

func mediaStageProbeJSON() string {
	return `{
  "format":{"format_name":"matroska","duration":"25.0","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"pix_fmt":"yuv420p","avg_frame_rate":"24000/1001"},
    {"index":1,"codec_type":"audio","codec_name":"aac","profile":"LC","sample_rate":"48000","channels":2,"channel_layout":"stereo","tags":{"language":"eng"}},
    {"index":3,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}}
  ]
}`
}

func aacSourceAudioProbeJSON() string {
	return `{
  "format":{"format_name":"matroska","duration":"12.0","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,"pix_fmt":"yuv420p10le","avg_frame_rate":"24000/1001"},
    {"index":1,"codec_type":"audio","codec_name":"aac","profile":"LC","sample_rate":"48000","channels":2,"channel_layout":"stereo","bit_rate":"128000","tags":{"language":"eng"}}
  ]
}`
}

func videoProbeJSON(width, height int) string {
	return videoProbeJSONWithFrameRate(width, height, "24000/1001")
}

func videoProbeJSONWithFrameRate(width, height int, frameRate string) string {
	return fmt.Sprintf(`{
  "format":{"format_name":"mov,mp4","duration":"12.0","size":"1234","bit_rate":"8000","probe_score":100},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"h264","width":%d,"height":%d,"pix_fmt":"yuv420p","avg_frame_rate":%q}
  ]
}`, width, height, frameRate)
}

func hdrVideoProbeJSON(width, height int) string {
	return fmt.Sprintf(`{
  "format":{"format_name":"mov,mp4","duration":"12.0","size":"1234","bit_rate":"12000000","probe_score":100},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"hevc","profile":"Main 10","width":%d,"height":%d,"pix_fmt":"yuv420p10le","avg_frame_rate":"24000/1001","color_transfer":"smpte2084","color_primaries":"bt2020","color_space":"bt2020nc"}
  ]
}`, width, height)
}

func keyframesJSON(values ...float64) string {
	var builder strings.Builder
	builder.WriteString(`{"frames":[`)
	for i, value := range values {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(fmt.Sprintf(`{"stream_index":0,"key_frame":1,"best_effort_timestamp_time":"%.6f"}`, value))
	}
	builder.WriteString(`]}`)
	return builder.String()
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}
