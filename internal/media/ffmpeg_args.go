package media

import (
	"fmt"
	"strconv"
	"strings"
)

type AudioTranscodeOutput struct {
	Output    string
	Selection AudioSelection
}

type AudioFileTranscodeOutput struct {
	Input     string
	Output    string
	Selection AudioSelection
}

type AudioTranscodeManySpec struct {
	Input   string
	Outputs []AudioTranscodeOutput
	Threads int
}

type AudioFileTranscodeManySpec struct {
	Outputs []AudioFileTranscodeOutput
	Threads int
}

type VideoTranscodeSpec struct {
	Input       string
	Output      string
	SourceIndex int
	Profile     VideoProfile
	Threads     int
	GOPSeconds  float64
}

type VideoRemuxSpec struct {
	Input            string
	Output           string
	SourceIndex      int
	Codec            string
	Threads          int
	StripDolbyVision bool
}

type SubtitleConvertOutput struct {
	Output      string
	SourceIndex int
	Codec       string
}

type SubtitleConvertManySpec struct {
	Input   string
	Outputs []SubtitleConvertOutput
	Threads int
}

type SpriteKeyframeExtractSpec struct {
	Input            string
	OutputGlob       string
	SourceIndex      int
	Width            int
	Height           int
	SeekSecond       float64
	KeyframeOrdinals []int
	DynamicRange     DynamicRange
	Threads          int
}

func BuildAudioTranscodeManyArgs(spec AudioTranscodeManySpec) ([]string, error) {
	if spec.Input == "" || len(spec.Outputs) == 0 {
		return nil, fmt.Errorf("audio transcode input and at least one output are required")
	}
	args := baseFFmpegArgs(spec.Input, spec.Threads)
	for _, output := range spec.Outputs {
		outputArgs, err := audioTranscodeOutputArgs(output)
		if err != nil {
			return nil, err
		}
		args = append(args, outputArgs...)
	}
	return args, nil
}

func audioTranscodeOutputArgs(spec AudioTranscodeOutput) ([]string, error) {
	if spec.Output == "" || spec.Selection.Source.Index < 0 {
		return nil, fmt.Errorf("audio transcode output and source index are required")
	}
	return audioTranscodeOutputArgsForMap(spec.Output, spec.Selection, streamMap(spec.Selection.Source.Index))
}

func audioTranscodeOutputArgsForMap(output string, selection AudioSelection, mapSpec string) ([]string, error) {
	if output == "" || selection.Source.Index < 0 || mapSpec == "" {
		return nil, fmt.Errorf("audio transcode output, source index, and stream map are required")
	}
	args := []string{"-map", mapSpec, "-vn", "-sn", "-dn"}
	if selection.Copy {
		args = append(args, "-c:a", "copy")
	} else {
		channels := selection.OutputChannels
		if channels <= 0 {
			channels = min(selection.Source.Channels, 2)
		}
		bitrate := selection.OutputBitrate
		if bitrate <= 0 {
			bitrate = DefaultAACBitrate(channels)
		}
		args = append(args,
			"-c:a", "aac",
			"-profile:a", "aac_low",
			"-b:a", bitrateArg(bitrate),
			"-ac", strconv.Itoa(max(channels, 1)),
		)
	}
	args = append(args, output)
	return args, nil
}

func BuildAudioFileTranscodeManyArgs(spec AudioFileTranscodeManySpec) ([]string, error) {
	if len(spec.Outputs) == 0 {
		return nil, fmt.Errorf("audio file transcode at least one output is required")
	}
	args := []string{"-hide_banner", "-nostdin", "-y"}
	if spec.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(spec.Threads))
	}
	for _, output := range spec.Outputs {
		if output.Input == "" {
			return nil, fmt.Errorf("audio file transcode input is required")
		}
		args = append(args, "-i", output.Input)
	}
	for index, output := range spec.Outputs {
		outputArgs, err := audioTranscodeOutputArgsForMap(output.Output, output.Selection, audioFileStreamMap(index))
		if err != nil {
			return nil, err
		}
		args = append(args, outputArgs...)
	}
	return args, nil
}

func BuildVideoTranscodeArgs(spec VideoTranscodeSpec) ([]string, error) {
	if spec.Input == "" || spec.Output == "" || spec.SourceIndex < 0 || spec.Profile.Name == "" {
		return nil, fmt.Errorf("video transcode input, output, source index, and profile are required")
	}
	if spec.Profile.Width <= 0 || spec.Profile.Height <= 0 || spec.Profile.AverageBitrate <= 0 || spec.Profile.PeakBitrate <= 0 || spec.Profile.BufferSize <= 0 {
		return nil, fmt.Errorf("video profile has invalid dimensions or bitrate")
	}
	args := baseFFmpegArgs(spec.Input, spec.Threads)
	args = append(args, "-map", streamMap(spec.SourceIndex), "-an", "-sn", "-dn")
	scale := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:force_divisible_by=2", spec.Profile.Width, spec.Profile.Height)
	args = append(args, "-vf", scale)
	switch strings.ToLower(spec.Profile.Codec) {
	case "h264", "avc":
		args = append(args, "-c:v", "libx264", "-profile:v", firstNonEmpty(spec.Profile.EncoderProfile, "high"))
	case "hevc", "h265":
		args = append(args, "-c:v", "libx265", "-profile:v", firstNonEmpty(spec.Profile.EncoderProfile, "main"), "-tag:v", "hvc1")
	default:
		return nil, fmt.Errorf("unsupported output video codec %q", spec.Profile.Codec)
	}
	args = append(args,
		"-pix_fmt", firstNonEmpty(spec.Profile.PixelFormat, "yuv420p"),
		"-b:v", bitrateArg(spec.Profile.AverageBitrate),
		"-maxrate", bitrateArg(spec.Profile.PeakBitrate),
		"-bufsize", bitrateArg(spec.Profile.BufferSize),
		"-preset", videoPreset(spec.Profile),
	)
	if spec.GOPSeconds > 0 {
		args = append(args,
			"-force_key_frames", "expr:gte(t,n_forced*"+trimFloat(spec.GOPSeconds)+")",
			"-sc_threshold", "0",
			"-flags", "+cgop",
		)
		if params := gopEncoderParams(spec.GOPSeconds, spec.Profile.FrameRate); params != "" {
			switch strings.ToLower(spec.Profile.Codec) {
			case "h264", "avc":
				args = append(args, "-x264-params", params)
			case "hevc", "h265":
				args = append(args, "-x265-params", params)
			}
		}
	}
	if spec.Profile.FrameRate > 0 {
		args = append(args, "-r", trimFloat(spec.Profile.FrameRate))
	}
	args = append(args, spec.Output)
	return args, nil
}

func BuildVideoRemuxArgs(spec VideoRemuxSpec) ([]string, error) {
	if spec.Input == "" || spec.Output == "" || spec.SourceIndex < 0 {
		return nil, fmt.Errorf("video remux input, output, and source index are required")
	}
	args := baseFFmpegArgs(spec.Input, spec.Threads)
	args = append(args, "-map", streamMap(spec.SourceIndex), "-map_chapters", "-1", "-map_metadata", "-1", "-an", "-sn", "-dn", "-c:v", "copy")
	if spec.StripDolbyVision {
		if !strings.EqualFold(strings.TrimSpace(spec.Codec), "hevc") && !strings.EqualFold(strings.TrimSpace(spec.Codec), "h265") {
			return nil, fmt.Errorf("Dolby Vision base-layer extraction requires HEVC input")
		}
		args = append(args, "-bsf:v", "filter_units=remove_types=62|63")
	}
	switch strings.ToLower(strings.TrimSpace(spec.Codec)) {
	case "h264", "avc":
		args = append(args, "-tag:v", "avc1")
	case "hevc", "h265":
		args = append(args, "-tag:v", "hvc1")
	}
	args = append(args, spec.Output)
	return args, nil
}

func BuildSubtitleConvertManyArgs(spec SubtitleConvertManySpec) ([]string, error) {
	if spec.Input == "" || len(spec.Outputs) == 0 {
		return nil, fmt.Errorf("subtitle conversion input and at least one output are required")
	}
	args := baseFFmpegArgs(spec.Input, spec.Threads)
	for _, output := range spec.Outputs {
		outputArgs, err := subtitleConvertOutputArgs(output)
		if err != nil {
			return nil, err
		}
		args = append(args, outputArgs...)
	}
	return args, nil
}

func subtitleConvertOutputArgs(spec SubtitleConvertOutput) ([]string, error) {
	if spec.Output == "" || spec.SourceIndex < 0 {
		return nil, fmt.Errorf("subtitle conversion output and source index are required")
	}
	if !IsTextSubtitleCodec(spec.Codec) {
		return nil, fmt.Errorf("subtitle codec %q is not text based", spec.Codec)
	}
	return []string{"-map", streamMap(spec.SourceIndex), "-vn", "-an", "-dn", "-c:s", "webvtt", "-f", "webvtt", spec.Output}, nil
}

func BuildSpriteKeyframeExtractArgs(spec SpriteKeyframeExtractSpec) ([]string, error) {
	if spec.Input == "" || spec.OutputGlob == "" || spec.SourceIndex < 0 || spec.Width <= 0 || spec.Height <= 0 || len(spec.KeyframeOrdinals) == 0 {
		return nil, fmt.Errorf("sprite keyframe extraction input, output, stream, size, and keyframes are required")
	}
	if spec.DynamicRange == DynamicRangeDolby {
		return nil, errDolbyVisionSpriteUnsupported()
	}
	args := []string{"-hide_banner", "-nostdin", "-y"}
	if spec.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(spec.Threads))
	}
	if spec.SeekSecond > 0 {
		args = append(args, "-ss", trimFloat(spec.SeekSecond))
	}
	args = append(args, "-skip_frame", "nokey", "-i", spec.Input, "-map", streamMap(spec.SourceIndex), "-an", "-sn", "-dn")
	selectTerms := make([]string, 0, len(spec.KeyframeOrdinals))
	for _, ordinal := range spec.KeyframeOrdinals {
		if ordinal < 0 {
			return nil, fmt.Errorf("sprite keyframe ordinal must be non-negative")
		}
		selectTerms = append(selectTerms, fmt.Sprintf("eq(n,%d)", ordinal))
	}
	filters := []string{fmt.Sprintf("select='%s'", strings.Join(selectTerms, "+"))}
	scale := spriteScaleFilter(spec.Width, spec.Height)
	if spriteNeedsToneMap(spec.DynamicRange) {
		filters = append(filters, hdrToSDRFilterChain()...)
		filters = append(filters, scale)
	} else {
		filters = append(filters, scale)
	}
	filters = append(filters,
		fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black", spec.Width, spec.Height),
		"format=rgb24",
	)
	args = append(args, "-vf", strings.Join(filters, ","), "-fps_mode", "vfr", "-frames:v", strconv.Itoa(len(spec.KeyframeOrdinals)), spec.OutputGlob)
	return args, nil
}

func errDolbyVisionSpriteUnsupported() error {
	return fmt.Errorf("Dolby Vision sprite extraction requires a Dolby Vision-aware renderer; direct ffmpeg extraction is disabled because it can produce color-inaccurate thumbnails")
}

func spriteNeedsToneMap(dynamicRange DynamicRange) bool {
	switch dynamicRange {
	case DynamicRangeHDR10, DynamicRangeHDR10Plus, DynamicRangeHLG:
		return true
	default:
		return false
	}
}

func spriteScaleFilter(width, height int) string {
	return fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:force_divisible_by=2", width, height)
}

func IsTextSubtitleCodec(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "subrip", "srt", "ass", "ssa", "webvtt", "mov_text", "text":
		return true
	default:
		return false
	}
}

func baseFFmpegArgs(input string, threads int) []string {
	args := []string{"-hide_banner", "-nostdin", "-y"}
	if threads > 0 {
		args = append(args, "-threads", strconv.Itoa(threads))
	}
	return append(args, "-i", input)
}

func streamMap(index int) string {
	return "0:" + strconv.Itoa(index)
}

func audioFileStreamMap(index int) string {
	return strconv.Itoa(index) + ":a:0"
}

func bitrateArg(value int64) string {
	return strconv.FormatInt(value, 10)
}

func trimFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
