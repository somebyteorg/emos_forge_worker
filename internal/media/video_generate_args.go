package media

import (
	"fmt"
	"strconv"
	"strings"
)

type VideoAcceleration string

const (
	VideoAccelerationCUDA   VideoAcceleration = "cuda"
	VideoAccelerationVulkan VideoAcceleration = "vulkan"
)

type VideoGenerateSpec struct {
	Input        string
	SourceIndex  int
	Videos       []VideoGenerateOutput
	Encoder      VideoEncoder
	Acceleration VideoAcceleration
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
	return buildVideoGenerateArgs(spec, nil, nil)
}

func buildVideoGenerateArgs(spec VideoGenerateSpec, inputArgs, outputArgs []string) ([]string, error) {
	if err := spec.Encoder.Validate(); err != nil {
		return nil, err
	}
	if spec.Input == "" || spec.SourceIndex < 0 {
		return nil, fmt.Errorf("video generate input and source index are required")
	}
	if len(spec.Videos) == 0 {
		return nil, fmt.Errorf("at least one generated video output is required")
	}
	if spec.Acceleration != "" && spec.Encoder.Backend != VideoEncoderNVENC {
		return nil, fmt.Errorf("%s video acceleration requires the NVENC encoder", spec.Acceleration)
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
		if spec.Acceleration == VideoAccelerationCUDA && output.ToneMap {
			return nil, fmt.Errorf("HDR to SDR tone mapping requires Vulkan acceleration or software video filters")
		}
		if spec.Acceleration == VideoAccelerationVulkan && !output.ToneMap {
			return nil, fmt.Errorf("Vulkan video acceleration requires HDR to SDR tone mapping")
		}
		chain := videoFilterChain(output, spec.Encoder, spec.Acceleration)
		filters = append(filters, fmt.Sprintf("%s%s[%s]", inputs[nextInput], strings.Join(chain, ","), label))
		nextInput++
	}

	deviceArgs := spec.Encoder.deviceArgs(spec.Acceleration)
	args := baseFFmpegArgs(spec.Input, 0, append(deviceArgs, inputArgs...)...)
	args = append(args, "-filter_complex", strings.Join(filters, ";"))
	for i, output := range spec.Videos {
		videoArgs, err := generatedVideoOutputArgs(output, spec.Encoder, spec.Acceleration)
		if err != nil {
			return nil, err
		}
		args = append(args, "-map", "["+videoLabels[i]+"]", "-an", "-sn", "-dn")
		args = append(args, videoArgs...)
		args = append(args, outputArgs...)
		args = append(args, output.Output)
	}
	return args, nil
}

func videoFilterChain(output VideoGenerateOutput, encoder VideoEncoder, acceleration VideoAcceleration) []string {
	profile := output.Profile
	filters := make([]string, 0, 5)
	if profile.FrameRate > 0 {
		filters = append(filters, "fps="+trimFloat(profile.FrameRate))
	}
	dimensions := fmt.Sprintf("%d:%d:force_original_aspect_ratio=decrease:force_divisible_by=2", profile.Width, profile.Height)
	// Allocate separate output frames so the filters cannot exhaust the decoder's frame pool.
	cudaScale := "scale_cuda=" + dimensions + ":interp_algo=bicubic:passthrough=0:format="
	switch acceleration {
	case VideoAccelerationCUDA:
		return append(filters, cudaScale+hardwareVideoFrameFormat(profile))
	case VideoAccelerationVulkan:
		pixelFormat := hardwareVideoFrameFormat(profile)
		// Transfer the resized frames through system memory between CUDA and Vulkan.
		filters = append(filters, cudaScale+"p010le", "hwdownload", "format=p010le",
			"libplacebo=format="+pixelFormat+":colorspace=bt709:color_trc=bt709:color_primaries=bt709:range=tv:tonemapping=hable:gamut_mode=clip:peak_detect=0",
			"format="+pixelFormat)
		return append(filters, deleteHDRMetadataFilters()...)
	}
	if output.ToneMap {
		filters = append(filters, hdrToSDRFilterChain()...)
	}
	filters = append(filters, "scale="+dimensions)
	return append(filters, encoder.filterArgs(profile)...)
}

func generatedVideoOutputArgs(output VideoGenerateOutput, encoder VideoEncoder, acceleration VideoAcceleration) ([]string, error) {
	profile := output.Profile
	args := make([]string, 0, 18)
	hardware := encoder.Hardware()
	var codec, cpuEncoder, encoderProfile string
	switch strings.ToLower(profile.Codec) {
	case "h264", "avc":
		codec, cpuEncoder, encoderProfile = "h264", "libx264", "high"
	case "hevc", "h265":
		codec, cpuEncoder, encoderProfile = "hevc", "libx265", "main"
	default:
		return nil, fmt.Errorf("unsupported output video codec %q", profile.Codec)
	}
	if hardware {
		args = append(args, encoder.hardwareOutputArgs(codec, profile, acceleration)...)
	} else {
		args = append(args, "-c:v", cpuEncoder)
	}
	args = append(args, "-profile:v", firstNonEmpty(profile.EncoderProfile, encoderProfile))
	if codec == "hevc" {
		args = append(args, "-tag:v", "hvc1")
	}
	if !hardware {
		if output.Threads > 0 {
			args = append(args, "-threads", strconv.Itoa(output.Threads))
		}
		args = append(args, "-pix_fmt", firstNonEmpty(profile.PixelFormat, "yuv420p"))
	}
	args = append(args,
		"-b:v", bitrateArg(profile.AverageBitrate),
		"-maxrate", bitrateArg(profile.PeakBitrate),
		"-bufsize", bitrateArg(profile.BufferSize),
	)
	if !hardware {
		args = append(args, "-preset", videoPreset(profile))
	}
	if output.ToneMap {
		args = append(args,
			"-color_primaries", "bt709",
			"-color_trc", "bt709",
			"-colorspace", "bt709",
			"-color_range", "tv",
		)
	}
	if output.GOPSeconds > 0 {
		args = append(args, "-force_key_frames", "expr:gte(t,n_forced*"+trimFloat(output.GOPSeconds)+")")
		if hardware {
			return append(args, encoder.hardwareGOPArgs(output)...), nil
		}
		args = append(args, "-sc_threshold", "0", "-flags", "+cgop")
		frameRate := output.GOPFrameRate
		if frameRate <= 0 {
			frameRate = profile.FrameRate
		}
		params := gopEncoderParams(output.GOPSeconds, frameRate)
		if codec == "h264" {
			args = append(args, "-x264-params", params)
		} else {
			args = append(args, "-x265-params", params)
		}
	}
	return args, nil
}

func hdrToSDRFilterChain() []string {
	return append([]string{
		"zscale=t=linear:npl=100",
		"tonemap=hable:desat=0",
		"zscale=t=bt709:m=bt709:p=bt709:r=tv",
	}, deleteHDRMetadataFilters()...)
}

func deleteHDRMetadataFilters() []string {
	return []string{
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
	if keyint := gopFrameCount(gopSeconds, frameRate); keyint > 0 {
		params = append([]string{fmt.Sprintf("keyint=%d", keyint), fmt.Sprintf("min-keyint=%d", keyint)}, params...)
	}
	return strings.Join(params, ":")
}

func gopFrameCount(gopSeconds, frameRate float64) int {
	if gopSeconds <= 0 || frameRate <= 0 {
		return 0
	}
	return int(gopSeconds*frameRate + 0.5)
}
