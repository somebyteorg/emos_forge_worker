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

	"forge_worker/internal/task"
)

func TestExecutorPackageModeCopiesAudioCodec(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := packageExecutorRequest(t, input)
	ensureExecutorPlan(t, db, request)

	executor := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: sourceAudioProbeJSON(), keyframesStdout: keyframesJSON(0, 2.5, 5, 7.5, 10)},
		CommandRunner: &fakeCommandRunner{t: t}, RetryInitial: time.Millisecond,
	})
	if err := executor.Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	artifactKinds := artifactKindMap(artifacts)
	if artifactKinds["audio_intermediate"] != "tmp/audio/audio_01_eng_eac3.mp4" {
		t.Fatalf("unexpected package audio artifact: %+v", artifacts)
	}
	if artifactKinds["video_intermediate"] != "tmp/video/video_package.mp4" {
		t.Fatalf("unexpected package video artifact: %+v", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "audio_intermediate" {
			continue
		}
		selection, err := audioSelectionFromArtifact(artifact)
		if err != nil {
			t.Fatalf("audioSelectionFromArtifact: %v", err)
		}
		if !selection.Copy || selection.OutputCodec != "eac3" || selection.OutputChannels != 6 {
			t.Fatalf("package audio was not copied: %+v", selection)
		}
		var outputManifest struct {
			AudioTracks []struct {
				Codec   string `json:"codec"`
				Profile string `json:"profile"`
			} `json:"audio_tracks"`
		}
		data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json"))
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		if err := json.Unmarshal(data, &outputManifest); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		if len(outputManifest.AudioTracks) != 1 || outputManifest.AudioTracks[0].Codec != "eac3" || outputManifest.AudioTracks[0].Profile != "" {
			t.Fatalf("unexpected manifest audio tracks: %+v", outputManifest.AudioTracks)
		}
		return
	}
	t.Fatalf("audio intermediate metadata not found")
}

func TestExecutorSelectAudioOutputsAllTracksWithSingleFFmpegInput(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8613",
		Input:    task.Input{Type: task.InputLocal, URI: input},
		Output:   task.Output{Root: t.TempDir()},
		Steps: task.StepRequests{
			Audio: task.AudioRequest{Enabled: true, Strategy: "one_per_language"},
		},
	}
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner: fakeProbeRunner{stdout: multiAudioProbeJSON()}, CommandRunner: runner,
		CPULimit: 4, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.ffmpegAudioArgs) != 1 {
		t.Fatalf("expected one audio ffmpeg command, got %d: %#v", len(runner.ffmpegAudioArgs), runner.ffmpegAudioArgs)
	}
	args := runner.ffmpegAudioArgs[0]
	joined := strings.Join(args, " ")
	for _, value := range []string{"-threads 4", "-map 0:1", "-map 0:2", "tmp/audio/audio_01_eng_eac3.mp4", "tmp/audio/audio_02_jpn_aac.m4a"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("audio command missing %q: %s", value, joined)
		}
	}
	if countArg(args, "-i") != 1 {
		t.Fatalf("audio command should read input once: %#v", args)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	audioPaths := artifactPathsByKind(artifacts, "audio_intermediate")
	if !slices.Equal(audioPaths, []string{"tmp/audio/audio_01_eng_eac3.mp4", "tmp/audio/audio_02_jpn_aac.m4a"}) {
		t.Fatalf("unexpected audio artifacts: %+v", artifacts)
	}
	commands, err := db.ListStepCommands(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListStepCommands: %v", err)
	}
	var audioSelectSummary string
	for _, command := range commands {
		if command.StepName == StepAudioSelect {
			audioSelectSummary = command.Summary
			break
		}
	}
	for _, value := range []string{"audio_select 2 tracks", "eng eac3 copy", "jpn aac copy"} {
		if !strings.Contains(audioSelectSummary, value) {
			t.Fatalf("audio select summary missing %q: %s", value, audioSelectSummary)
		}
	}
}

func TestExecutorAudioAACTranscodesNonAACSelection(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := packageExecutorRequest(t, input)
	request.Steps.Audio.AAC = true
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: sourceAudioProbeJSON(), keyframesStdout: keyframesJSON(0, 2.5, 5, 7.5, 10)},
		CommandRunner: runner, CPULimit: 4, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	states := stepStateMap(mustListSteps(t, db, request.TaskUUID))
	if states[StepAudioSelect] != string(task.StepSucceeded) || states[StepAudioAAC] != string(task.StepSucceeded) {
		t.Fatalf("unexpected audio step states: %+v", states)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	artifactKinds := artifactKindMap(artifacts)
	if artifactKinds["audio_intermediate"] != "tmp/audio/audio_01_eng_eac3.mp4" || artifactKinds["audio_aac_intermediate"] != "tmp/audio/audio_01_eng_aac.m4a" {
		t.Fatalf("unexpected audio artifacts: %+v", artifacts)
	}
	audioPackagedPaths := artifactPathsByKind(artifacts, "audio_packaged")
	if !slices.Equal(audioPackagedPaths, []string{"audio/01_eng_aac/init.mp4", "audio/01_eng_eac3/init.mp4"}) {
		t.Fatalf("expected package and AAC audio init segments, got %+v", audioPackagedPaths)
	}
	audioSegmentPaths := artifactPathsByKind(artifacts, "audio_segment")
	if !slices.Equal(audioSegmentPaths, []string{"audio/01_eng_aac/00001.m4s", "audio/01_eng_eac3/00001.m4s"}) {
		t.Fatalf("expected package and AAC audio media segments, got %+v", audioSegmentPaths)
	}
	aacArtifact := findArtifact(t, artifacts, "audio_aac_intermediate")
	selection, err := audioSelectionFromArtifact(aacArtifact)
	if err != nil {
		t.Fatalf("audioSelectionFromArtifact: %v", err)
	}
	if selection.Copy || selection.OutputCodec != "aac" || selection.OutputProfile != "lc" || selection.OutputChannels != 6 || selection.OutputBitrate != 384_000 {
		t.Fatalf("unexpected AAC selection: %+v", selection)
	}
	if len(runner.ffmpegAudioArgs) != 2 {
		t.Fatalf("expected audio extract + AAC transcode ffmpeg commands, got %d: %#v", len(runner.ffmpegAudioArgs), runner.ffmpegAudioArgs)
	}
	extractArgs := runner.ffmpegAudioArgs[0]
	aacArgs := runner.ffmpegAudioArgs[1]
	if !containsArgPair(extractArgs, "-threads", "4") || !containsArgPair(aacArgs, "-threads", "4") {
		t.Fatalf("audio commands should use configured CPU limit: %#v", runner.ffmpegAudioArgs)
	}
	if countArg(extractArgs, "-i") != 1 || countArg(aacArgs, "-i") != 1 {
		t.Fatalf("audio commands should each read once: %#v", runner.ffmpegAudioArgs)
	}
	extractedInput := filepath.Join(request.Output.Root, request.TaskUUID, "tmp", "audio", "audio_01_eng_eac3.mp4")
	if !containsArgPair(aacArgs, "-i", extractedInput) || containsArgPair(aacArgs, "-i", input) {
		t.Fatalf("AAC command should read the extracted audio track, not the source video: %#v", aacArgs)
	}
	joinedExtract := strings.Join(extractArgs, " ")
	for _, value := range []string{"-map 0:1", "-c:a copy", "tmp/audio/audio_01_eng_eac3.mp4"} {
		if !strings.Contains(joinedExtract, value) {
			t.Fatalf("audio extract command missing %q: %s", value, joinedExtract)
		}
	}
	joinedAAC := strings.Join(aacArgs, " ")
	for _, value := range []string{"-map 0:a:0", "-c:a aac", "-b:a 384000", "-ac 6", "tmp/audio/audio_01_eng_aac.m4a"} {
		if !strings.Contains(joinedAAC, value) {
			t.Fatalf("AAC command missing %q: %s", value, joinedAAC)
		}
	}
	if len(runner.packagerArgs) != 1 {
		t.Fatalf("packager args = %d, want 1", len(runner.packagerArgs))
	}
	joinedPackager := strings.Join(runner.packagerArgs[0], " ")
	for _, value := range []string{"tmp/audio/audio_01_eng_eac3.mp4", "tmp/audio/audio_01_eng_aac.m4a"} {
		if !strings.Contains(joinedPackager, value) {
			t.Fatalf("packager should include package and AAC audio artifacts, missing %q: %s", value, joinedPackager)
		}
	}
	commands, err := db.ListStepCommands(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListStepCommands: %v", err)
	}
	var packageSummary string
	for _, command := range commands {
		if command.StepName == StepAudioPackage {
			packageSummary = command.Summary
			break
		}
	}
	for _, value := range []string{"package 3 tracks", "audio_01_eng_aac", "audio_01_eng_eac3"} {
		if !strings.Contains(packageSummary, value) {
			t.Fatalf("package summary should show all packaged audio tracks, missing %q: %s", value, packageSummary)
		}
	}

	var outputManifest struct {
		AudioTracks []struct {
			Codec    string `json:"codec"`
			Profile  string `json:"profile"`
			Channels int    `json:"channels"`
			Bitrate  int64  `json:"bitrate"`
		} `json:"audio_tracks"`
	}
	data, err := os.ReadFile(filepath.Join(request.Output.Root, request.TaskUUID, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &outputManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(outputManifest.AudioTracks) != 2 {
		t.Fatalf("unexpected manifest AAC audio tracks: %+v", outputManifest.AudioTracks)
	}
	byCodec := make(map[string]struct {
		Codec    string `json:"codec"`
		Profile  string `json:"profile"`
		Channels int    `json:"channels"`
		Bitrate  int64  `json:"bitrate"`
	}, len(outputManifest.AudioTracks))
	for _, track := range outputManifest.AudioTracks {
		byCodec[track.Codec] = track
	}
	if byCodec["eac3"].Codec != "eac3" || byCodec["eac3"].Channels != 6 || byCodec["eac3"].Bitrate != 640_000 {
		t.Fatalf("manifest should include packaged source audio: %+v", outputManifest.AudioTracks)
	}
	if byCodec["aac"].Codec != "aac" || byCodec["aac"].Profile != "lc" || byCodec["aac"].Channels != 6 || byCodec["aac"].Bitrate != 384_000 {
		t.Fatalf("manifest should include AAC audio: %+v", outputManifest.AudioTracks)
	}
}

func TestExecutorAudioAACTranscodesSelectionsInOneFFmpegCommand(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := packageExecutorRequest(t, input)
	request.TaskUUID = "019f61e1-eb9d-7a90-adba-3a6f7ecc8614"
	request.Steps.Audio.AAC = true
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: multiNonAACAudioProbeJSON(), keyframesStdout: keyframesJSON(0, 2.5, 5, 7.5, 10)},
		CommandRunner: runner, CPULimit: 4, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.ffmpegAudioArgs) != 2 {
		t.Fatalf("expected audio extract + one batch AAC transcode, got %d: %#v", len(runner.ffmpegAudioArgs), runner.ffmpegAudioArgs)
	}
	extractArgs := runner.ffmpegAudioArgs[0]
	if countArg(extractArgs, "-i") != 1 || !containsArgPair(extractArgs, "-map", "0:1") || !containsArgPair(extractArgs, "-map", "0:2") {
		t.Fatalf("audio select should extract all tracks from the source in one command: %#v", extractArgs)
	}
	aacArgs := runner.ffmpegAudioArgs[1]
	joinedAAC := strings.Join(aacArgs, " ")
	for _, inputName := range []string{"audio_01_eng_eac3.mp4", "audio_02_jpn_ac3.mp4"} {
		inputPath := filepath.Join(request.Output.Root, request.TaskUUID, "tmp", "audio", inputName)
		if !containsArgPair(aacArgs, "-i", inputPath) {
			t.Fatalf("AAC batch command missing input %s: %#v", inputPath, aacArgs)
		}
	}
	for _, value := range []string{
		"-map 0:a:0", "-map 1:a:0", "audio_01_eng_aac.m4a", "audio_02_jpn_aac.m4a",
	} {
		if !strings.Contains(joinedAAC, value) {
			t.Fatalf("AAC batch command missing %q: %s", value, joinedAAC)
		}
	}
	if countArg(aacArgs, "-i") != 2 || containsArgPair(aacArgs, "-i", input) {
		t.Fatalf("AAC batch command should read both extracted audio tracks and not the source video: %#v", aacArgs)
	}
	commands, err := db.ListStepCommands(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListStepCommands: %v", err)
	}
	var aacSummaries []string
	for _, command := range commands {
		if command.StepName == StepAudioAAC {
			aacSummaries = append(aacSummaries, command.Summary)
		}
	}
	if len(aacSummaries) != 1 {
		t.Fatalf("expected one AAC command summary, got %+v", aacSummaries)
	}
	for _, want := range []string{"audio_aac batch 2 tracks", "eng eac3 -> aac", "jpn ac3 -> aac"} {
		if !strings.Contains(aacSummaries[0], want) {
			t.Fatalf("AAC summary missing %q: %s", want, aacSummaries[0])
		}
	}
}

func TestExecutorAudioAACOnlyDoesNotPackageSourceAudio(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := packageExecutorRequest(t, input)
	request.TaskUUID = "019f61e1-eb9d-7a90-adba-3a6f7ecc8619"
	request.Steps.Audio.Package = false
	request.Steps.Audio.AAC = true
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: sourceAudioProbeJSON(), keyframesStdout: keyframesJSON(0, 2.5, 5, 7.5, 10)},
		CommandRunner: runner, CPULimit: 4, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	audioPackagedPaths := artifactPathsByKind(artifacts, "audio_packaged")
	if !slices.Equal(audioPackagedPaths, []string{"audio/01_eng_aac/init.mp4"}) {
		t.Fatalf("AAC-only request should package only AAC audio, got %+v", audioPackagedPaths)
	}
	if len(runner.packagerArgs) != 1 {
		t.Fatalf("packager args = %d, want 1", len(runner.packagerArgs))
	}
	joinedPackager := strings.Join(runner.packagerArgs[0], " ")
	if !strings.Contains(joinedPackager, "tmp/audio/audio_01_eng_aac.m4a") || strings.Contains(joinedPackager, "tmp/audio/audio_01_eng_eac3.mp4") {
		t.Fatalf("AAC-only packager should not include source audio: %s", joinedPackager)
	}
}

func TestExecutorAudioAACSkipsAACSelection(t *testing.T) {
	db := openExecutorDB(t)
	input := filepath.Join(t.TempDir(), "source.mkv")
	writeTestFile(t, input, []byte("media"))
	request := packageExecutorRequest(t, input)
	request.Steps.Audio.AAC = true
	ensureExecutorPlan(t, db, request)

	runner := &recordingCommandRunner{fakeCommandRunner: fakeCommandRunner{t: t}}
	if err := NewExecutor(db, Options{
		ProbeRunner:   fakeProbeRunner{stdout: aacSourceAudioProbeJSON(), keyframesStdout: keyframesJSON(0, 2.5, 5, 7.5, 10)},
		CommandRunner: runner, RetryInitial: time.Millisecond,
	}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run: %v", err)
	}
	states := stepStateMap(mustListSteps(t, db, request.TaskUUID))
	if states[StepAudioSelect] != string(task.StepSucceeded) || states[StepAudioAAC] != string(task.StepSkipped) {
		t.Fatalf("unexpected audio step states: %+v", states)
	}
	artifacts, err := db.ListArtifacts(context.Background(), request.TaskUUID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	artifactKinds := artifactKindMap(artifacts)
	if artifactKinds["audio_intermediate"] != "tmp/audio/audio_01_eng_aac.m4a" || artifactKinds["audio_aac_intermediate"] != "" {
		t.Fatalf("unexpected AAC skip artifacts: %+v", artifacts)
	}
	if len(runner.ffmpegAudioArgs) != 1 || !containsArgPair(runner.ffmpegAudioArgs[0], "-c:a", "copy") {
		t.Fatalf("AAC source should only be copied by audio select: %#v", runner.ffmpegAudioArgs)
	}
	if len(runner.packagerArgs) != 1 || !strings.Contains(strings.Join(runner.packagerArgs[0], " "), "tmp/audio/audio_01_eng_aac.m4a") {
		t.Fatalf("packager should use selected AAC artifact: %#v", runner.packagerArgs)
	}
}
