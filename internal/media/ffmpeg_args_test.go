package media

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBuildAudioTranscodeArgsForAACEncode(t *testing.T) {
	args, err := BuildAudioTranscodeManyArgs(AudioTranscodeManySpec{
		Input: "/input.mkv", Threads: 2,
		Outputs: []AudioTranscodeOutput{{
			Output: "/out/audio_eng.m4a",
			Selection: AudioSelection{
				Source: AudioTrack{Index: 2, Channels: 6}, OutputBitrate: 384_000, OutputChannels: 6,
			},
		}},
	})
	if err != nil {
		t.Fatalf("BuildAudioTranscodeArgs: %v", err)
	}
	want := []string{"-hide_banner", "-nostdin", "-y", "-threads", "2", "-i", "/input.mkv", "-map", "0:2", "-vn", "-sn", "-dn", "-c:a", "aac", "-profile:a", "aac_low", "-b:a", "384000", "-ac", "6", "/out/audio_eng.m4a"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildMetadataProbeArgs(t *testing.T) {
	args, err := BuildMetadataProbeArgs("/out/master.m3u8")
	if err != nil {
		t.Fatalf("BuildMetadataProbeArgs: %v", err)
	}
	want := []string{"-allowed_extensions", "ALL", "-show_format", "-show_streams", "-print_format", "json", "/out/master.m3u8"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildAudioTranscodeArgsForCopy(t *testing.T) {
	args, err := BuildAudioTranscodeManyArgs(AudioTranscodeManySpec{
		Input: "/input.mkv",
		Outputs: []AudioTranscodeOutput{{
			Output: "/out/audio.m4a", Selection: AudioSelection{Source: AudioTrack{Index: 1}, Copy: true},
		}},
	})
	if err != nil {
		t.Fatalf("BuildAudioTranscodeArgs: %v", err)
	}
	if !slices.Contains(args, "copy") || slices.Contains(args, "-b:a") {
		t.Fatalf("unexpected copy args: %#v", args)
	}
}

func TestBuildAudioTranscodeArgsDefaultsAACBitrateByChannels(t *testing.T) {
	args, err := BuildAudioTranscodeManyArgs(AudioTranscodeManySpec{
		Input: "/input.mkv",
		Outputs: []AudioTranscodeOutput{{
			Output: "/out/audio.m4a", Selection: AudioSelection{Source: AudioTrack{Index: 1, Channels: 6}, OutputChannels: 6},
		}},
	})
	if err != nil {
		t.Fatalf("BuildAudioTranscodeArgs: %v", err)
	}
	if !containsArgPair(args, "-b:a", "384000") || !containsArgPair(args, "-ac", "6") {
		t.Fatalf("expected 5.1 AAC default bitrate args: %#v", args)
	}
}

func TestBuildAudioTranscodeManyArgsCombinesOutputs(t *testing.T) {
	args, err := BuildAudioTranscodeManyArgs(AudioTranscodeManySpec{
		Input:   "/input.mkv",
		Threads: 4,
		Outputs: []AudioTranscodeOutput{
			{Output: "/out/audio_01_eng_eac3.mp4", Selection: AudioSelection{Source: AudioTrack{Index: 1, Codec: "eac3"}, Copy: true}},
			{Output: "/out/audio_02_jpn_aac.m4a", Selection: AudioSelection{Source: AudioTrack{Index: 2, Channels: 2}, OutputCodec: "aac", OutputChannels: 2, OutputBitrate: 128_000}},
		},
	})
	if err != nil {
		t.Fatalf("BuildAudioTranscodeManyArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, value := range []string{"-i /input.mkv", "-map 0:1 -vn -sn -dn -c:a copy /out/audio_01_eng_eac3.mp4", "-map 0:2 -vn -sn -dn -c:a aac", "/out/audio_02_jpn_aac.m4a"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("args missing %q: %s", value, joined)
		}
	}
	if countArg(args, "-i") != 1 || !containsArgPair(args, "-threads", "4") {
		t.Fatalf("audio many args should read input once and use configured threads: %#v", args)
	}
}

func TestBuildAudioFileTranscodeManyArgsCombinesExtractedInputs(t *testing.T) {
	args, err := BuildAudioFileTranscodeManyArgs(AudioFileTranscodeManySpec{
		Threads: 4,
		Outputs: []AudioFileTranscodeOutput{
			{Input: "/work/audio_01_eng_eac3.mp4", Output: "/out/audio_01_eng_aac.m4a", Selection: AudioSelection{Source: AudioTrack{Index: 1, Channels: 6}, OutputCodec: "aac", OutputChannels: 6, OutputBitrate: 384_000}},
			{Input: "/work/audio_02_jpn_eac3.mp4", Output: "/out/audio_02_jpn_aac.m4a", Selection: AudioSelection{Source: AudioTrack{Index: 2, Channels: 2}, OutputCodec: "aac", OutputChannels: 2, OutputBitrate: 128_000}},
		},
	})
	if err != nil {
		t.Fatalf("BuildAudioFileTranscodeManyArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, value := range []string{"-i /work/audio_01_eng_eac3.mp4", "-i /work/audio_02_jpn_eac3.mp4", "-map 0:a:0 -vn -sn -dn -c:a aac", "/out/audio_01_eng_aac.m4a", "-map 1:a:0 -vn -sn -dn -c:a aac", "/out/audio_02_jpn_aac.m4a"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("args missing %q: %s", value, joined)
		}
	}
	if countArg(args, "-i") != 2 || !containsArgPair(args, "-threads", "4") {
		t.Fatalf("audio file args should read each extracted track and use configured threads: %#v", args)
	}
}

func TestBuildVideoTranscodeArgs(t *testing.T) {
	args, err := BuildVideoTranscodeArgs(VideoTranscodeSpec{
		Input: "/input.mkv", Output: "/out/video_1080p.mp4", SourceIndex: 0, GOPSeconds: 2.5,
		Profile: VideoProfile{Name: "1080p", Codec: "hevc", EncoderProfile: "main10", Width: 1920, Height: 1080, AverageBitrate: 7_000_000, PeakBitrate: 10_000_000, BufferSize: 20_000_000, PixelFormat: "yuv420p10le"},
	})
	if err != nil {
		t.Fatalf("BuildVideoTranscodeArgs: %v", err)
	}
	wantItems := []string{"-nostdin", "-map", "0:0", "-c:v", "libx265", "-profile:v", "main10", "-tag:v", "hvc1", "-pix_fmt", "yuv420p10le", "-preset", "medium", "-maxrate", "10000000", "-force_key_frames", "expr:gte(t,n_forced*2.5)", "-sc_threshold", "0", "/out/video_1080p.mp4"}
	for _, item := range wantItems {
		if !slices.Contains(args, item) {
			t.Fatalf("expected %q in args %#v", item, args)
		}
	}
}

func TestBuildVideoTranscodeArgsCanSetFrameRate(t *testing.T) {
	args, err := BuildVideoTranscodeArgs(VideoTranscodeSpec{
		Input: "/input.mkv", Output: "/out/video_720p.mp4", SourceIndex: 0, GOPSeconds: 10,
		Profile: VideoProfile{Name: "720p", Codec: "h264", EncoderProfile: "high", Width: 1280, Height: 720, FrameRate: 30, AverageBitrate: 3_200_000, PeakBitrate: 4_500_000, BufferSize: 9_000_000, PixelFormat: "yuv420p"},
	})
	if err != nil {
		t.Fatalf("BuildVideoTranscodeArgs: %v", err)
	}
	if !containsArgPair(args, "-r", "30") {
		t.Fatalf("expected output frame rate in args %#v", args)
	}
}

func TestBuildVideoGenerateArgsCombinesVideos(t *testing.T) {
	args, err := BuildVideoGenerateArgs(VideoGenerateSpec{
		Input: "/input.mkv", SourceIndex: 0,
		Videos: []VideoGenerateOutput{
			{Output: "/out/video_720p.mp4", Threads: 1, GOPSeconds: 10, GOPFrameRate: 30, Profile: VideoProfile{Name: "720p", Codec: "h264", EncoderProfile: "high", Width: 1280, Height: 720, FrameRate: 30, AverageBitrate: 3_200_000, PeakBitrate: 4_500_000, BufferSize: 9_000_000, PixelFormat: "yuv420p"}},
			{Output: "/out/video_1080p.mp4", Threads: 3, GOPSeconds: 10, Profile: VideoProfile{Name: "1080p", Codec: "hevc", EncoderProfile: "main", Width: 1920, Height: 1080, AverageBitrate: 6_000_000, PeakBitrate: 10_000_000, BufferSize: 20_000_000, PixelFormat: "yuv420p"}},
		},
	})
	if err != nil {
		t.Fatalf("BuildVideoGenerateArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, value := range []string{
		"split=2", "fps=30,scale=1280:720",
		"-c:v libx264", "-threads 1", "-c:v libx265", "-threads 3", "-preset fast", "-preset medium", "-force_key_frames expr:gte(t,n_forced*10)", "-flags +cgop",
		"-x264-params keyint=300:min-keyint=300:scenecut=0:open-gop=0", "-x265-params scenecut=0:open-gop=0",
		"/out/video_720p.mp4", "/out/video_1080p.mp4",
	} {
		if !strings.Contains(joined, value) {
			t.Fatalf("args missing %q: %s", value, joined)
		}
	}
	if strings.Contains(joined, "frame_%") {
		t.Fatalf("video generate args should not include sprite output: %s", joined)
	}
	inputIndex := slices.Index(args, "-i")
	if inputIndex < 0 {
		t.Fatalf("args missing input: %#v", args)
	}
	if slices.Contains(args[:inputIndex], "-threads") {
		t.Fatalf("video generate should not force input decoder threads before -i: %#v", args)
	}
}

func TestBuildVideoGenerateArgsToneMapsCompatibilityOutput(t *testing.T) {
	args, err := BuildVideoGenerateArgs(VideoGenerateSpec{
		Input: "/input.mkv", SourceIndex: 0,
		Videos: []VideoGenerateOutput{
			{Output: "/out/video_720p.mp4", GOPSeconds: 10, ToneMap: true, Profile: VideoProfile{Name: "720p", Codec: "h264", EncoderProfile: "high", Width: 1280, Height: 720, AverageBitrate: 3_200_000, PeakBitrate: 4_500_000, BufferSize: 9_000_000, PixelFormat: "yuv420p", DynamicRange: DynamicRangeSDR}},
		},
	})
	if err != nil {
		t.Fatalf("BuildVideoGenerateArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, value := range []string{
		"zscale=t=linear:npl=100", "tonemap=hable", "zscale=t=bt709:m=bt709:p=bt709:r=tv",
		"sidedata=mode=delete:type=MASTERING_DISPLAY_METADATA", "sidedata=mode=delete:type=CONTENT_LIGHT_LEVEL",
		"sidedata=mode=delete:type=DYNAMIC_HDR_PLUS", "sidedata=mode=delete:type=DOVI_RPU_BUFFER", "sidedata=mode=delete:type=DOVI_METADATA",
		"-c:v libx264", "-profile:v high", "-pix_fmt yuv420p",
		"-color_primaries bt709", "-color_trc bt709", "-colorspace bt709", "-color_range tv",
	} {
		if !strings.Contains(joined, value) {
			t.Fatalf("args missing %q: %s", value, joined)
		}
	}
}

func TestBuildVideoRemuxArgs(t *testing.T) {
	args, err := BuildVideoRemuxArgs(VideoRemuxSpec{Input: "/input.mkv", Output: "/out/source.mp4", SourceIndex: 0, Codec: "hevc", Threads: 4})
	if err != nil {
		t.Fatalf("BuildVideoRemuxArgs: %v", err)
	}
	want := []string{"-hide_banner", "-nostdin", "-y", "-threads", "4", "-i", "/input.mkv", "-map", "0:0", "-map_chapters", "-1", "-map_metadata", "-1", "-an", "-sn", "-dn", "-c:v", "copy", "-tag:v", "hvc1", "/out/source.mp4"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildVideoRemuxArgsExtractsDolbyVisionHDR10BaseLayer(t *testing.T) {
	args, err := BuildVideoRemuxArgs(VideoRemuxSpec{
		Input: "/input.mkv", Output: "/out/source.mp4", SourceIndex: 0,
		Codec: "hevc", Threads: 4, StripDolbyVision: true,
	})
	if err != nil {
		t.Fatalf("BuildVideoRemuxArgs: %v", err)
	}
	if !containsArgPair(args, "-bsf:v", "filter_units=remove_types=62|63") || !containsArgPair(args, "-tag:v", "hvc1") {
		t.Fatalf("Dolby Vision base-layer extraction args are incomplete: %#v", args)
	}
}

func TestBuildVideoRemuxArgsTagsH264(t *testing.T) {
	args, err := BuildVideoRemuxArgs(VideoRemuxSpec{Input: "/input.mkv", Output: "/out/source.mp4", SourceIndex: 0, Codec: "h264", Threads: 4})
	if err != nil {
		t.Fatalf("BuildVideoRemuxArgs: %v", err)
	}
	want := []string{"-hide_banner", "-nostdin", "-y", "-threads", "4", "-i", "/input.mkv", "-map", "0:0", "-map_chapters", "-1", "-map_metadata", "-1", "-an", "-sn", "-dn", "-c:v", "copy", "-tag:v", "avc1", "/out/source.mp4"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildSubtitleConvertArgsRejectsImageSubtitle(t *testing.T) {
	_, err := BuildSubtitleConvertManyArgs(SubtitleConvertManySpec{
		Input: "/input.mkv", Outputs: []SubtitleConvertOutput{{Output: "/out/sub.vtt", SourceIndex: 3, Codec: "hdmv_pgs_subtitle"}},
	})
	if err == nil {
		t.Fatalf("expected image subtitle to be rejected")
	}

	args, err := BuildSubtitleConvertManyArgs(SubtitleConvertManySpec{
		Input: "/input.mkv", Outputs: []SubtitleConvertOutput{{Output: "/out/sub.vtt", SourceIndex: 4, Codec: "subrip"}},
	})
	if err != nil {
		t.Fatalf("BuildSubtitleConvertArgs text: %v", err)
	}
	if !slices.Contains(args, "webvtt") || !slices.Contains(args, "0:4") {
		t.Fatalf("unexpected subtitle args: %#v", args)
	}
}

func TestBuildSubtitleConvertManyArgsCombinesOutputs(t *testing.T) {
	args, err := BuildSubtitleConvertManyArgs(SubtitleConvertManySpec{
		Input:   "/input.mkv",
		Threads: 4,
		Outputs: []SubtitleConvertOutput{
			{Output: "/out/sub_03_eng.vtt", SourceIndex: 3, Codec: "subrip"},
			{Output: "/out/sub_04_zho.vtt", SourceIndex: 4, Codec: "ass"},
		},
	})
	if err != nil {
		t.Fatalf("BuildSubtitleConvertManyArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, value := range []string{"-i /input.mkv", "-map 0:3 -vn -an -dn -c:s webvtt -f webvtt /out/sub_03_eng.vtt", "-map 0:4 -vn -an -dn -c:s webvtt -f webvtt /out/sub_04_zho.vtt"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("args missing %q: %s", value, joined)
		}
	}
	if countArg(args, "-i") != 1 || !containsArgPair(args, "-threads", "4") {
		t.Fatalf("subtitle many args should read input once and use configured threads: %#v", args)
	}
}

func TestBuildSpriteKeyframeExtractArgs(t *testing.T) {
	args, err := BuildSpriteKeyframeExtractArgs(SpriteKeyframeExtractSpec{
		Input: "in.mkv", OutputGlob: "frame_%06d.png", SourceIndex: 0, Width: 320, Height: 180,
		SeekSecond: 10.2, KeyframeOrdinals: []int{0, 4, 9}, DynamicRange: DynamicRangeHDR10, Threads: 2,
	})
	if err != nil {
		t.Fatalf("BuildSpriteKeyframeExtractArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, value := range []string{"-threads 2", "-ss 10.2", "-skip_frame nokey", "-map 0:0", "select='eq(n,0)+eq(n,4)+eq(n,9)'", "tonemap=hable", "zscale=t=bt709:m=bt709:p=bt709:r=tv", "scale=320:180", "-frames:v 3", "frame_%06d.png"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("args missing %q: %s", value, joined)
		}
	}
	if strings.Index(joined, "tonemap=hable") > strings.Index(joined, "scale=320:180") {
		t.Fatalf("sprite tone mapping should happen before scaling: %s", joined)
	}
}

func TestBuildSpriteKeyframeExtractArgsRejectsDolbyVision(t *testing.T) {
	_, err := BuildSpriteKeyframeExtractArgs(SpriteKeyframeExtractSpec{
		Input: "in.mkv", OutputGlob: "frame_%06d.png", SourceIndex: 0, Width: 320, Height: 180,
		KeyframeOrdinals: []int{0, 4, 9}, DynamicRange: DynamicRangeDolby,
	})
	if err == nil || !strings.Contains(err.Error(), "Dolby Vision sprite extraction requires") {
		t.Fatalf("expected Dolby Vision sprite extraction to be rejected, got %v", err)
	}
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
