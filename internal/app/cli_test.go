package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge_worker/internal/config"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

const (
	testVersion        = "test-version"
	testBuildTime      = "2026-07-17T12:34:56Z"
	testVersionOutput  = "version: test-version\nbuild_time: 2026-07-17T12:34:56Z\n"
	defaultVideo       = "package"
	defaultSpriteSizes = "1280x720,640x360,320x180"
)

func TestVersionOutputsMatch(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		if output := runAppForTest(t, args...); output != testVersionOutput {
			t.Fatalf("unexpected version output: %q", output)
		}
	}
}

func runAppForTest(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), args, testVersion, testBuildTime, &stdout, &stderr); err != nil {
		t.Fatalf("Run %v: %v", args, err)
	}
	return stdout.String()
}

func assertCommandOrder(t *testing.T, text string, commands []string) {
	t.Helper()
	last := -1
	for _, command := range commands {
		index := strings.Index(text, "\n  "+command)
		if index == -1 {
			t.Fatalf("command %q missing from help:\n%s", command, text)
		}
		if index <= last {
			t.Fatalf("command %q is out of order in help:\n%s", command, text)
		}
		last = index
	}
}

func TestHelpOutput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		commands []string
	}{
		{name: "root", commands: []string{"doctor", "local", "upload", "worker", "version", "help", "completion"}},
		{name: "local", args: []string{"local", "--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := runAppForTest(t, tt.args...)
			if !strings.Contains(text, "Usage:") {
				t.Fatalf("help should include Usage section:\n%s", text)
			}
			assertCommandOrder(t, text, tt.commands)
		})
	}
}

func TestZshCompletionScript(t *testing.T) {
	text := runAppForTest(t, "completion", "zsh")
	for _, want := range []string{"#compdef forge-worker", "__forge-worker", "completion"} {
		if !strings.Contains(text, want) {
			t.Fatalf("zsh completion output missing %q:\n%s", want, text)
		}
	}
}

func TestUploadPrompts(t *testing.T) {
	jobUUID, err := promptUploadJobUUID(strings.NewReader("job-123\n"), io.Discard)
	if err != nil {
		t.Fatalf("promptUploadJobUUID: %v", err)
	}
	if jobUUID != "job-123" {
		t.Fatalf("job uuid = %q", jobUUID)
	}

	root := t.TempDir()
	candidate := filepath.Join(root, "job-output")
	if err := os.Mkdir(candidate, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	selected, err := promptUploadRoot(strings.NewReader("1\n"), root, io.Discard)
	if err != nil {
		t.Fatalf("promptUploadRoot select: %v", err)
	}
	if selected != candidate {
		t.Fatalf("selected root = %q, want %q", selected, candidate)
	}

	typed, err := promptUploadRoot(strings.NewReader("/manual/output\n"), filepath.Join(root, "missing"), io.Discard)
	if err != nil {
		t.Fatalf("promptUploadRoot manual: %v", err)
	}
	if typed != "/manual/output" {
		t.Fatalf("typed root = %q", typed)
	}

	if promptUploadDeleteArtifacts(strings.NewReader("\n"), io.Discard) {
		t.Fatalf("delete uploaded files prompt should default to false")
	}
	if !promptUploadDeleteArtifacts(strings.NewReader("y\n"), io.Discard) {
		t.Fatalf("delete uploaded files prompt should accept yes")
	}
}

func TestUploadPromptUsesSharedReader(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "job-output")
	if err := os.Mkdir(candidate, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reader := bufio.NewReader(strings.NewReader("job-123\n1\ny\n"))
	jobUUID, err := promptUploadJobUUID(reader, io.Discard)
	if err != nil {
		t.Fatalf("promptUploadJobUUID: %v", err)
	}
	selected, err := promptUploadRoot(reader, root, io.Discard)
	if err != nil {
		t.Fatalf("promptUploadRoot: %v", err)
	}
	deleteArtifacts := promptUploadDeleteArtifacts(reader, io.Discard)
	if jobUUID != "job-123" || selected != candidate || !deleteArtifacts {
		t.Fatalf("prompt values = jobUUID=%q selected=%q delete=%t", jobUUID, selected, deleteArtifacts)
	}
}

func TestResolveUploadDeleteArtifactsDefaultsToFalseIndependentOfEnv(t *testing.T) {
	if resolveUploadDeleteArtifacts(uploadOptions{}, false, strings.NewReader("y\n"), io.Discard) {
		t.Fatalf("non-interactive manual upload should default delete artifacts to false")
	}
	if !resolveUploadDeleteArtifacts(uploadOptions{DeleteArtifacts: true, DeleteArtifactsSet: true}, false, strings.NewReader("\n"), io.Discard) {
		t.Fatalf("explicit delete-artifacts flag should enable deletion")
	}
	if !resolveUploadDeleteArtifacts(uploadOptions{}, true, strings.NewReader("y\n"), io.Discard) {
		t.Fatalf("interactive prompt should be able to enable deletion")
	}
}

func TestManualUploadRequiresEncryptedVideo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{
		"playback": {"encryption": {"scheme": "none"}},
		"video_tracks": [{"media_id": "video_1080p"}]
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := validateManualUploadManifest(root); err == nil || !strings.Contains(err.Error(), "unencrypted video") {
		t.Fatalf("expected unencrypted video to be rejected, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{
		"playback": {"encryption": {"scheme": "cbcs"}},
		"video_tracks": [{"media_id": "video_1080p"}]
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := validateManualUploadManifest(root); err != nil {
		t.Fatalf("encrypted video should be allowed: %v", err)
	}
}

func TestLocalDoesNotAcceptLegacySingleDashLongFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"local", "-input", "/var/forge-worker-test/input.mkv"}, testVersion, testBuildTime, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown shorthand flag") {
		t.Fatalf("expected Cobra shorthand flag error, got %v", err)
	}
}

func TestTaskObserverPrintsProgressAndCommands(t *testing.T) {
	ctx := context.Background()
	database := state.New()
	request := task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8610",
		Input:    task.Input{Type: task.InputLocal, URI: "/var/forge-worker-test/source.mkv"},
		Output:   task.Output{Root: t.TempDir()},
	}
	if _, err := database.EnsureTask(ctx, request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	if err := database.EnsureSteps(ctx, request.TaskUUID, []state.StepSpec{
		{Name: "prepare", Kind: "prepare", Weight: 1, MaxAttempts: 3},
	}); err != nil {
		t.Fatalf("EnsureSteps: %v", err)
	}

	var output bytes.Buffer
	observer := newTaskObserver(database, request.TaskUUID, &output)
	observer.refresh(ctx)
	if !strings.Contains(output.String(), "task "+request.TaskUUID+" | discovered | total 0.0%") {
		t.Fatalf("initial task status missing:\n%s", output.String())
	}

	if err := database.TransitionTaskTo(ctx, request.TaskUUID, task.StatePreparing); err != nil {
		t.Fatalf("TransitionTaskTo: %v", err)
	}
	if err := database.StartStep(ctx, request.TaskUUID, "prepare"); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if err := database.UpdateStepProgress(ctx, request.TaskUUID, "prepare", 44); err != nil {
		t.Fatalf("UpdateStepProgress: %v", err)
	}
	if err := database.UpdateStepPerformance(ctx, request.TaskUUID, "prepare", 3.9, 0.15); err != nil {
		t.Fatalf("UpdateStepPerformance: %v", err)
	}
	if err := database.UpdateStepCommandSummary(ctx, request.TaskUUID, "prepare", "ffmpeg -version"); err != nil {
		t.Fatalf("UpdateStepCommandSummary: %v", err)
	}
	if err := database.UpdateStepCommandSummary(ctx, request.TaskUUID, "prepare", "ffprobe -version"); err != nil {
		t.Fatalf("UpdateStepCommandSummary second command: %v", err)
	}
	observer.refresh(ctx)

	text := output.String()
	if !strings.Contains(text, "task "+request.TaskUUID+" | preparing | total 44.0% | current prepare") {
		t.Fatalf("updated task progress missing:\n%s", text)
	}
	if !strings.Contains(text, "step prepare | running | 44.0% | 3.9 fps | 0.15x | attempt 1/3") {
		t.Fatalf("step progress missing:\n%s", text)
	}
	if strings.Contains(text, "Prepare workspace") {
		t.Fatalf("step display name should not duplicate the canonical name:\n%s", text)
	}
	if !strings.Contains(text, "cmd prepare | ffmpeg -version") {
		t.Fatalf("first command summary missing:\n%s", text)
	}
	if !strings.Contains(text, "cmd prepare | ffprobe -version") {
		t.Fatalf("second command summary missing:\n%s", text)
	}
}

func TestRunObservedTaskPrintsProgressAndPreservesError(t *testing.T) {
	ctx := context.Background()
	database := state.New()
	request := task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8619",
		Input:    task.Input{Type: task.InputLocal, URI: "/var/forge-worker-test/source.mkv"},
		Output:   task.Output{Root: t.TempDir()},
	}
	if _, err := database.EnsureTask(ctx, request, task.StateDiscovered); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	if err := database.EnsureSteps(ctx, request.TaskUUID, []state.StepSpec{
		{Name: "prepare", Kind: "prepare", Weight: 1, MaxAttempts: 3},
	}); err != nil {
		t.Fatalf("EnsureSteps: %v", err)
	}

	wantErr := errors.New("pipeline stopped")
	var output bytes.Buffer
	err := runObservedTask(ctx, database, request.TaskUUID, &output, func() error {
		if err := database.TransitionTaskTo(ctx, request.TaskUUID, task.StatePreparing); err != nil {
			return err
		}
		if err := database.StartStep(ctx, request.TaskUUID, "prepare"); err != nil {
			return err
		}
		if err := database.UpdateStepProgress(ctx, request.TaskUUID, "prepare", 55); err != nil {
			return err
		}
		if err := database.UpdateStepCommandSummary(ctx, request.TaskUUID, "prepare", "ffmpeg -version"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runObservedTask error = %v, want %v", err, wantErr)
	}
	text := output.String()
	for _, want := range []string{
		"task " + request.TaskUUID + " | preparing | total 55.0% | current prepare",
		"step prepare | running | 55.0% | attempt 1/3",
		"cmd prepare | ffmpeg -version",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("observed task output missing %q:\n%s", want, text)
		}
	}
}

func TestLocalCommandSummaryShowsHumanPrefixForCustomCommands(t *testing.T) {
	audio := localCommandSummary("audio_aac batch 2 tracks | eng eac3 -> aac, jpn ac3 -> aac | ffmpeg -hide_banner -i /very/long/input")
	if audio != "audio_aac batch 2 tracks | eng eac3 -> aac, jpn ac3 -> aac" {
		t.Fatalf("audio summary = %q", audio)
	}
	selected := localCommandSummary("audio_select 2 tracks | eng eac3 copy, jpn aac copy | ffmpeg -hide_banner -i /very/long/input")
	if selected != "audio_select 2 tracks | eng eac3 copy, jpn aac copy" {
		t.Fatalf("select summary = %q", selected)
	}
	packaged := localCommandSummary("package 3 tracks | video video_package | audio audio_01_eng_aac,audio_01_eng_eac3 | packager in=/very/long/input")
	if packaged != "package 3 tracks | video video_package | audio audio_01_eng_aac,audio_01_eng_eac3" {
		t.Fatalf("package summary = %q", packaged)
	}
	metadata := localCommandSummary("metadata probe | audio/01_eng_aac/index.m3u8 | ffprobe -show_format /very/long/input")
	if metadata != "metadata probe | audio/01_eng_aac/index.m3u8" {
		t.Fatalf("metadata summary = %q", metadata)
	}
	raw := localCommandSummary("ffmpeg -version")
	if raw != "ffmpeg -version" {
		t.Fatalf("raw summary = %q", raw)
	}
}

func TestStepStatusLineFormatsDownloadProgress(t *testing.T) {
	line := stepStatusLine(state.StepRecord{
		Name: "download_url", State: string(task.StepRunning), Progress: 40,
		TransferredBytes: 4 << 20, TotalBytes: 10 << 20, BytesPerSecond: 2 << 20, ETASeconds: 4.2,
		Attempt: 1, MaxAttempts: 3,
	})
	want := "step download_url | running | 40.0% | 4.0MiB/10.0MiB | 2.0MiB/s | eta 5s | attempt 1/3"
	if line != want {
		t.Fatalf("line = %q, want %q", line, want)
	}
}

func TestTaskProgressDescriptionLabelsDownloadPercent(t *testing.T) {
	description := taskProgressDescription(taskSnapshot{
		Task: state.TaskRecord{State: task.StateDownloading, Progress: 4},
		Steps: []state.StepRecord{{
			Name: "download_url", State: string(task.StepRunning), Progress: 40,
			TransferredBytes: 4 << 20, TotalBytes: 10 << 20, BytesPerSecond: 2 << 20, ETASeconds: 3,
		}},
	})
	want := "downloading total 4.0% download 40.0% 4.0MiB/10.0MiB 2.0MiB/s eta 3s"
	if description != want {
		t.Fatalf("description = %q, want %q", description, want)
	}
}

func TestLocalCommandSupportsScriptFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newLocalCommand(context.Background(), &stdout, &stderr)
	for _, name := range []string{"input", "uuid", "output", "video", "video-profiles", "audio", "audio-rules", "audio-strategy", "sprites", "sprite-sizes", "subtitles", "encrypt"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("local command should expose --%s", name)
		}
	}
	for _, name := range []string{"env-file", "encryption-mode", "cpu-limit"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Fatalf("local command should still not expose --%s", name)
		}
	}
	if err := cmd.ParseFlags([]string{"--subtitles=false"}); err != nil {
		t.Fatalf("parse --subtitles=false: %v", err)
	}
	if got := cmd.Flags().Lookup("subtitles").Value.String(); got != "false" {
		t.Fatalf("--subtitles value = %q, want false", got)
	}
}

func TestUploadCommandSupportsDeleteArtifactsFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newUploadCommand(context.Background(), &stdout, &stderr)
	for _, name := range []string{"root", "job-uuid", "delete-artifacts"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("upload command should expose --%s", name)
		}
	}
}

type promptFields struct {
	Input         string
	TaskUUID      string
	VideoProfiles string
	AudioRules    string
	SpriteSizes   string
	Subtitles     bool
	Audio         bool
	Video         bool
	Sprites       bool
}

func promptDefaults(input, taskUUID string) promptFields {
	return promptFields{
		Input:         input,
		TaskUUID:      taskUUID,
		VideoProfiles: defaultVideo,
		AudioRules:    "package,aac",
		SpriteSizes:   defaultSpriteSizes,
		Subtitles:     true,
		Audio:         true,
		Video:         true,
		Sprites:       true,
	}
}

func defaultPromptFields() promptFields {
	return promptDefaults("/input.mkv", "019f61e1-eb9d-7a90-adba-3a6f7ecc8615")
}

func runPromptForTest(t *testing.T, fields *promptFields, input string, encrypt *bool) string {
	t.Helper()
	var stdout bytes.Buffer
	reader := bufio.NewReader(strings.NewReader(input))
	if err := promptLocalWithReader(
		reader,
		&fields.Input,
		&fields.TaskUUID,
		&fields.VideoProfiles,
		&fields.AudioRules,
		&fields.SpriteSizes,
		&fields.Subtitles,
		&fields.Audio,
		&fields.Video,
		&fields.Sprites,
		encrypt,
		&stdout,
		&stdout,
	); err != nil {
		t.Fatalf("promptLocalWithReader: %v", err)
	}
	return stdout.String()
}

func TestPromptLocalSubtitles(t *testing.T) {
	tests := []struct {
		name       string
		initial    bool
		input      string
		want       bool
		wantPrompt string
	}{
		{name: "default enabled", initial: true, input: "", want: true, wantPrompt: "extract text subtitles [y]:"},
		{name: "can disable", initial: true, input: "n\n", want: false, wantPrompt: "extract text subtitles [y]:"},
		{name: "can enable", initial: false, input: "y\n", want: true, wantPrompt: "extract text subtitles [n]:"},
		{name: "retries invalid value", initial: false, input: "maybe\ny\n", want: true, wantPrompt: "invalid value; enter y or n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := defaultPromptFields()
			fields.Subtitles = tt.initial
			fields.Video = false
			fields.Audio = false
			fields.Sprites = false
			output := runPromptForTest(t, &fields, "\n\n"+tt.input, nil)
			if fields.Subtitles != tt.want {
				t.Fatalf("subtitles = %t, want %t", fields.Subtitles, tt.want)
			}
			if !strings.Contains(output, tt.wantPrompt) {
				t.Fatalf("subtitle prompt missing %q:\n%s", tt.wantPrompt, output)
			}
		})
	}
}

func TestPromptLocalGeneratesTaskUUIDWhenBlank(t *testing.T) {
	fields := promptDefaults("", "")
	fields.Video = false
	fields.Audio = false
	fields.Sprites = false

	output := runPromptForTest(t, &fields, "/input.mkv\n\nn\nn\n\nn\n", nil)
	if !task.ValidUUID(fields.TaskUUID) {
		t.Fatalf("generated task uuid is invalid: %q", fields.TaskUUID)
	}
	if !strings.Contains(output, "generated task uuid: "+fields.TaskUUID) {
		t.Fatalf("generated task uuid was not shown:\n%s", output)
	}
}

func TestPromptLocalRetriesInvalidTaskUUID(t *testing.T) {
	fields := promptDefaults("", "")
	fields.Video = false
	fields.Audio = false
	fields.Sprites = false

	output := runPromptForTest(t, &fields, "/input.mkv\ninvalid\n\nn\nn\n\nn\n", nil)
	if !task.ValidUUID(fields.TaskUUID) {
		t.Fatalf("generated task uuid is invalid: %q", fields.TaskUUID)
	}
	if !strings.Contains(output, "invalid task uuid; enter a UUID or leave blank to generate one") {
		t.Fatalf("invalid task uuid was not rejected:\n%s", output)
	}
}

func TestPromptSelectionsRetryInvalidValues(t *testing.T) {
	tests := []struct {
		name       string
		prompt     func(*bufio.Reader, *os.File, io.Writer, string) (string, error)
		input      string
		current    string
		want       string
		wantOutput string
	}{
		{name: "video", prompt: promptVideoProfiles, input: "4k\n720p,package\n", current: "package", want: "720p,package", wantOutput: "invalid video profiles"},
		{name: "audio", prompt: promptAudioRules, input: "flac\npackage,aac\n", current: "package,aac", want: "package,aac", wantOutput: "invalid audio rules"},
		{name: "sprites", prompt: promptSpriteSizes, input: "640x0\n640x360\n", current: defaultSpriteSizes, want: "640x360", wantOutput: "invalid sprite sizes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := tt.prompt(bufio.NewReader(strings.NewReader(tt.input)), nil, &output, tt.current)
			if err != nil {
				t.Fatalf("prompt: %v", err)
			}
			if got != tt.want {
				t.Fatalf("value = %q, want %q", got, tt.want)
			}
			if !strings.Contains(output.String(), tt.wantOutput) {
				t.Fatalf("retry message missing %q:\n%s", tt.wantOutput, output.String())
			}
		})
	}
}

func assertContainsAll(t *testing.T, text string, values []string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("output missing %q:\n%s", value, text)
		}
	}
}

func assertContainsNone(t *testing.T, text string, values []string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(text, value) {
			t.Fatalf("output should not contain %q:\n%s", value, text)
		}
	}
}

func TestPromptLocalEncryption(t *testing.T) {
	tests := []struct {
		name         string
		fields       promptFields
		input        string
		encrypt      bool
		wantEncrypt  bool
		wantAudio    bool
		wantVideo    bool
		wantContains []string
		wantMissing  []string
	}{
		{
			name:        "default enabled",
			fields:      promptDefaults("", ""),
			input:       "/input.mkv\n\n\n\n\n\n\n\n\n",
			encrypt:     true,
			wantEncrypt: true,
			wantAudio:   true,
			wantVideo:   true,
			wantContains: []string{
				"task uuid (blank to generate):",
				"selected audio rules [package,aac]:",
				"enable ClearKey encryption [y]:",
				"enable thumbnail sprites [y]:",
			},
		},
		{
			name:        "can enable",
			fields:      defaultPromptFields(),
			input:       "\n\n\n\n\n\n\ny\n",
			encrypt:     false,
			wantEncrypt: true,
			wantAudio:   true,
			wantVideo:   true,
		},
		{
			name:        "hidden when audio and video disabled",
			fields:      defaultPromptFields(),
			input:       "n\nn\n\n\n",
			encrypt:     false,
			wantEncrypt: false,
			wantAudio:   false,
			wantVideo:   false,
			wantMissing: []string{"ClearKey"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := tt.fields
			encrypt := tt.encrypt
			output := runPromptForTest(t, &fields, tt.input, &encrypt)
			if fields.Input == "" {
				t.Fatalf("input should be populated: %#v", fields)
			}
			if encrypt != tt.wantEncrypt {
				t.Fatalf("encrypt = %t, want %t", encrypt, tt.wantEncrypt)
			}
			if fields.Audio != tt.wantAudio || fields.Video != tt.wantVideo {
				t.Fatalf("audio/video = %t/%t, want %t/%t", fields.Audio, fields.Video, tt.wantAudio, tt.wantVideo)
			}
			assertContainsAll(t, output, tt.wantContains)
			assertContainsNone(t, output, tt.wantMissing)
		})
	}
}

func TestPromptLocalVideoProfilesDefaultToPackage(t *testing.T) {
	input := "/input.mkv"
	taskUUID := "019f61e1-eb9d-7a90-adba-3a6f7ecc8616"
	videoProfiles := "package"
	audioRules := "package,aac"
	spriteSizes := "1280x720,640x360,320x180"
	audio := true
	video := true
	sprites := false
	var stdout bytes.Buffer
	reader := bufio.NewReader(strings.NewReader("\n\n\n\n\n"))

	subtitles := true
	if err := promptLocalWithReader(reader, &input, &taskUUID, &videoProfiles, &audioRules, &spriteSizes, &subtitles, &audio, &video, &sprites, nil, &stdout, &stdout); err != nil {
		t.Fatalf("promptLocalWithReader: %v", err)
	}
	if videoProfiles != "package" {
		t.Fatalf("video profiles = %q, want package", videoProfiles)
	}
	text := stdout.String()
	if !strings.Contains(text, "video profile options: package, 720p, 1080p") || !strings.Contains(text, "selected video profiles [package]:") {
		t.Fatalf("video profile prompt missing default package:\n%s", text)
	}
	if !strings.Contains(text, "audio rule options: package, aac") || !strings.Contains(text, "selected audio rules [package,aac]:") {
		t.Fatalf("audio rule prompt missing default package,aac:\n%s", text)
	}
}

func TestPromptLocalAcceptsVideoProfilesAndSpriteSizes(t *testing.T) {
	input := "/input.mkv"
	taskUUID := "019f61e1-eb9d-7a90-adba-3a6f7ecc8617"
	videoProfiles := "package"
	audioRules := "package,aac"
	spriteSizes := "1280x720,640x360,320x180"
	audio := true
	video := true
	sprites := true
	var stdout bytes.Buffer
	reader := bufio.NewReader(strings.NewReader("\n720p,package\n\n\n\n\n640x360\n"))

	subtitles := true
	if err := promptLocalWithReader(reader, &input, &taskUUID, &videoProfiles, &audioRules, &spriteSizes, &subtitles, &audio, &video, &sprites, nil, &stdout, &stdout); err != nil {
		t.Fatalf("promptLocalWithReader: %v", err)
	}
	if videoProfiles != "720p,package" {
		t.Fatalf("video profiles = %q, want 720p,package", videoProfiles)
	}
	if spriteSizes != "640x360" {
		t.Fatalf("sprite sizes = %q, want 640x360", spriteSizes)
	}
	if audioRules != "package,aac" {
		t.Fatalf("audio rules = %q, want package,aac", audioRules)
	}
	text := stdout.String()
	if !strings.Contains(text, "sprite size options: 1280x720,640x360,320x180") || !strings.Contains(text, "selected sprite sizes [1280x720,640x360,320x180]:") {
		t.Fatalf("sprite size prompt missing options:\n%s", text)
	}
	if strings.Contains(text, "sprite frame format") {
		t.Fatalf("sprite frame format should be configured through env, not prompt:\n%s", text)
	}
}

func TestBuildLocalRequestUsesPackageVideoProfile(t *testing.T) {
	cfg := config.Defaults()
	request, err := buildLocalRequest(cfg, "019f61e1-eb9d-7a90-adba-3a6f7ecc8611", "/input.mkv", "/output", "720p,package", "one_per_language", false, false, false, false, true, false, "", "")
	if err != nil {
		t.Fatalf("buildLocalRequest: %v", err)
	}
	want := []string{"720p", "package"}
	if len(request.Steps.Video.Profiles) != len(want) {
		t.Fatalf("profiles = %#v", request.Steps.Video.Profiles)
	}
	for i := range want {
		if request.Steps.Video.Profiles[i] != want[i] {
			t.Fatalf("profiles = %#v, want %#v", request.Steps.Video.Profiles, want)
		}
	}
}

func TestBuildLocalRequestSetsSpriteFrameFormat(t *testing.T) {
	cfg := config.Defaults()
	request, err := buildLocalRequest(cfg, "019f61e1-eb9d-7a90-adba-3a6f7ecc8614", "/input.mkv", "/output", "package", "one_per_language", false, false, false, false, false, true, "320x180", "ppm")
	if err != nil {
		t.Fatalf("buildLocalRequest: %v", err)
	}
	if request.Steps.Sprites.FrameFormat != "ppm" {
		t.Fatalf("sprite frame format = %q, want ppm", request.Steps.Sprites.FrameFormat)
	}
}

func TestBuildLocalRequestSetsAudioAAC(t *testing.T) {
	cfg := config.Defaults()
	request, err := buildLocalRequest(cfg, "019f61e1-eb9d-7a90-adba-3a6f7ecc8618", "/input.mkv", "/output", "package", "one_per_language", false, true, true, true, false, false, "", "")
	if err != nil {
		t.Fatalf("buildLocalRequest: %v", err)
	}
	if !request.Steps.Audio.Enabled || !request.Steps.Audio.Package || !request.Steps.Audio.AAC {
		t.Fatalf("audio request = %+v, want enabled AAC", request.Steps.Audio)
	}
}

func TestLocalAudioRules(t *testing.T) {
	tests := []struct {
		value     string
		wantAudio bool
		wantPack  bool
		wantAAC   bool
		wantErr   bool
	}{
		{value: "", wantAudio: true, wantPack: true, wantAAC: true},
		{value: "package", wantAudio: true, wantPack: true, wantAAC: false},
		{value: "aac", wantAudio: true, wantPack: false, wantAAC: true},
		{value: "package,aac", wantAudio: true, wantPack: true, wantAAC: true},
		{value: "none", wantAudio: false, wantAAC: false},
		{value: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			gotAudio, gotPack, gotAAC, err := audioSettingsFromRules(true, tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("audioSettingsFromRules: %v", err)
			}
			if gotAudio != tt.wantAudio || gotPack != tt.wantPack || gotAAC != tt.wantAAC {
				t.Fatalf("audio/package/aac = %t/%t/%t, want %t/%t/%t", gotAudio, gotPack, gotAAC, tt.wantAudio, tt.wantPack, tt.wantAAC)
			}
		})
	}
	gotAudio, gotPack, gotAAC, err := audioSettingsFromRules(false, "package,aac")
	if err != nil || gotAudio || gotPack || gotAAC {
		t.Fatalf("disabled audio should ignore rules: audio=%t package=%t aac=%t err=%v", gotAudio, gotPack, gotAAC, err)
	}
}

func TestWorkerPipelineDefaultsToClearKeyEncryption(t *testing.T) {
	cfg := config.Defaults()
	opt := pipelineOptions(cfg)
	if opt.EncryptionMode != "clearkey" {
		t.Fatalf("worker encryption mode = %q, want clearkey", opt.EncryptionMode)
	}
}

func TestLocalTaskOutputDirAppendsTaskUUID(t *testing.T) {
	request := task.Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8612",
		Output:   task.Output{Root: filepath.Join(string(filepath.Separator), "output")},
	}
	want := filepath.Join(string(filepath.Separator), "output", request.TaskUUID)
	if got := localTaskOutputDir(request); got != want {
		t.Fatalf("localTaskOutputDir = %q, want %q", got, want)
	}
}

func TestApplyLocalRuntimePathsUsesOutputForRuntimeDirs(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	cfg := config.Defaults()
	output := filepath.Join(dir, "custom-output")

	finalOutput, err := applyLocalRuntimePaths(&cfg, output)
	if err != nil {
		t.Fatalf("applyLocalRuntimePaths: %v", err)
	}
	if finalOutput != output || cfg.OutputDir != output {
		t.Fatalf("output = %q cfg=%q, want %q", finalOutput, cfg.OutputDir, output)
	}
	if err := cfg.EnsureRuntimeDirs(); err != nil {
		t.Fatalf("EnsureRuntimeDirs: %v", err)
	}
	for _, path := range []string{output} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("expected directory %s, info=%v err=%v", path, info, err)
		}
	}
	for _, path := range []string{filepath.Join(dir, "output")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unexpected runtime directory %s, err=%v", path, err)
		}
	}
}

func TestPromptKeyMultiSelect(t *testing.T) {
	tests := []struct {
		name       string
		keys       string
		choices    []promptChoice
		want       []string
		wantOutput []string
	}{
		{
			name: "space and arrows",
			keys: "\x1b[B \x1b[B \r",
			choices: []promptChoice{
				{Label: "720p", Value: "720p"},
				{Label: "1080p", Value: "1080p"},
				{Label: "package", Value: "package", Selected: true},
			},
			want:       []string{"720p", "1080p", "package"},
			wantOutput: []string{"space to toggle", "[x] 720p", "[x] 1080p"},
		},
		{
			name: "j and k",
			keys: "j k \n",
			choices: []promptChoice{
				{Label: "1280x720", Value: "1280x720"},
				{Label: "640x360", Value: "640x360"},
			},
			want: []string{"1280x720", "640x360"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			values, err := promptKeyMultiSelect(bufio.NewReader(strings.NewReader(tt.keys)), &stdout, "selected", tt.choices)
			if err != nil {
				t.Fatalf("promptKeyMultiSelect: %v", err)
			}
			assertStringSlice(t, values, tt.want)
			assertContainsAll(t, stdout.String(), tt.wantOutput)
		})
	}
}

func TestFormatLocalDuration(t *testing.T) {
	if got := formatLocalDuration(1500 * time.Millisecond); got != "2s" {
		t.Fatalf("formatLocalDuration = %s, want 2s", got)
	}
	if got := formatLocalDuration(123 * time.Millisecond); got != "123ms" {
		t.Fatalf("formatLocalDuration = %s, want 123ms", got)
	}
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("values = %#v, want %#v", got, want)
		}
	}
}

func withWorkingDirectory(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}
