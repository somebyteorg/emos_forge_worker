package media

import (
	"fmt"
	"strconv"
	"strings"
)

type VideoGenerateSpec struct {
	Input       string
	SourceIndex int
	Videos      []VideoGenerateOutput
}

type VideoGenerateOutput struct {
	Output       string
	Profile      VideoProfile
	Threads      int
	GOPSeconds   float64
	GOPFrameRate float64
	ToneMap      bool
}

func BuildVideoGenerateArgs(spec VideoGenerateSpec) ([]string, error) {
	if spec.Input == "" || spec.SourceIndex < 0 {
		return nil, fmt.Errorf("video generate input and source index are required")
	}
	if len(spec.Videos) == 0 {
		return nil, fmt.Errorf("at least one generated video output is required")
	}
	splitCount := len(spec.Videos)
	inputs := make([]string, 0, splitCount)
	filters := make([]string, 0, splitCount+1)
	if splitCount == 1 {
		inputs = append(inputs, fmt.Sprintf("[%s]", streamMap(spec.SourceIndex)))
	} else {
		inputLabels := make([]string, 0, splitCount)
		for i := 0; i < splitCount; i++ {
			label := fmt.Sprintf("[vg_in_%d]", i)
			inputLabels = append(inputLabels, label)
			inputs = append(inputs, label)
		}
		filters = append(filters, fmt.Sprintf("[%s]split=%d%s", streamMap(spec.SourceIndex), splitCount, strings.Join(inputLabels, "")))
	}
	videoLabels := make([]string, 0, len(spec.Videos))
	nextInput := 0
	for i, output := range spec.Videos {
		if output.Output == "" || output.Profile.Name == "" {
			return nil, fmt.Errorf("generated video output and profile are required")
		}
		if output.Profile.Width <= 0 || output.Profile.Height <= 0 || output.Profile.AverageBitrate <= 0 || output.Profile.PeakBitrate <= 0 || output.Profile.BufferSize <= 0 {
			return nil, fmt.Errorf("generated video profile has invalid dimensions or bitrate")
		}
		label := fmt.Sprintf("vg_video_%d", i)
		videoLabels = append(videoLabels, label)
		chain := videoFilterChain(output)
		filters = append(filters, fmt.Sprintf("%s%s[%s]", inputs[nextInput], strings.Join(chain, ","), label))
		nextInput++
	}

	args := baseFFmpegArgs(spec.Input, 0)
	args = append(args, "-filter_complex", strings.Join(filters, ";"))
	for i, output := range spec.Videos {
		videoArgs, err := generatedVideoOutputArgs(output)
		if err != nil {
			return nil, err
		}
		args = append(args, "-map", "["+videoLabels[i]+"]", "-an", "-sn", "-dn")
		args = append(args, videoArgs...)
		args = append(args, output.Output)
	}
	return args, nil
}

func videoFilterChain(output VideoGenerateOutput) []string {
	profile := output.Profile
	filters := make([]string, 0, 5)
	if profile.FrameRate > 0 {
		filters = append(filters, "fps="+trimFloat(profile.FrameRate))
	}
	if output.ToneMap {
		filters = append(filters, hdrToSDRFilterChain()...)
	}
	filters = append(filters, fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:force_divisible_by=2", profile.Width, profile.Height))
	return filters
}

func generatedVideoOutputArgs(output VideoGenerateOutput) ([]string, error) {
	profile := output.Profile
	args := make([]string, 0, 18)
	codec := strings.ToLower(profile.Codec)
	switch codec {
	case "h264", "avc":
		args = append(args, "-c:v", "libx264", "-profile:v", firstNonEmpty(profile.EncoderProfile, "high"))
	case "hevc", "h265":
		args = append(args, "-c:v", "libx265", "-profile:v", firstNonEmpty(profile.EncoderProfile, "main"), "-tag:v", "hvc1")
	default:
		return nil, fmt.Errorf("unsupported output video codec %q", profile.Codec)
	}
	if output.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(output.Threads))
	}
	args = append(args,
		"-pix_fmt", firstNonEmpty(profile.PixelFormat, "yuv420p"),
		"-b:v", bitrateArg(profile.AverageBitrate),
		"-maxrate", bitrateArg(profile.PeakBitrate),
		"-bufsize", bitrateArg(profile.BufferSize),
		"-preset", videoPreset(profile),
	)
	if output.ToneMap {
		args = append(args,
			"-color_primaries", "bt709",
			"-color_trc", "bt709",
			"-colorspace", "bt709",
			"-color_range", "tv",
		)
	}
	if output.GOPSeconds > 0 {
		args = append(args,
			"-force_key_frames", "expr:gte(t,n_forced*"+trimFloat(output.GOPSeconds)+")",
			"-sc_threshold", "0",
			"-flags", "+cgop",
		)
		frameRate := output.GOPFrameRate
		if frameRate <= 0 {
			frameRate = profile.FrameRate
		}
		if params := gopEncoderParams(output.GOPSeconds, frameRate); params != "" {
			switch codec {
			case "h264", "avc":
				args = append(args, "-x264-params", params)
			case "hevc", "h265":
				args = append(args, "-x265-params", params)
			}
		}
	}
	return args, nil
}

func hdrToSDRFilterChain() []string {
	return []string{
		"zscale=t=linear:npl=100",
		"tonemap=hable:desat=0",
		"zscale=t=bt709:m=bt709:p=bt709:r=tv",
		"sidedata=mode=delete:type=MASTERING_DISPLAY_METADATA",
		"sidedata=mode=delete:type=CONTENT_LIGHT_LEVEL",
		"sidedata=mode=delete:type=DYNAMIC_HDR_PLUS",
		"sidedata=mode=delete:type=DOVI_RPU_BUFFER",
		"sidedata=mode=delete:type=DOVI_METADATA",
	}
}

func videoPreset(profile VideoProfile) string {
	if profile.Name == "1080p" {
		return "medium"
	}
	return "fast"
}

func gopEncoderParams(gopSeconds, frameRate float64) string {
	params := []string{"scenecut=0", "open-gop=0"}
	if gopSeconds > 0 && frameRate > 0 {
		keyint := int(gopSeconds*frameRate + 0.5)
		if keyint > 0 {
			params = append([]string{fmt.Sprintf("keyint=%d", keyint), fmt.Sprintf("min-keyint=%d", keyint)}, params...)
		}
	}
	return strings.Join(params, ":")
}
