package media

import (
	"fmt"
	"path/filepath"
	"strconv"
)

const (
	VideoEncoderCPU   = "cpu"
	VideoEncoderNVENC = "nvenc"
	VideoEncoderQSV   = "qsv"
	VideoEncoderVAAPI = "vaapi"
)

type VideoEncoder struct {
	Backend string
	Device  string
}

func (e VideoEncoder) Validate() error {
	switch e.Backend {
	case "", VideoEncoderCPU:
		return nil
	case VideoEncoderNVENC:
		if e.Device != "" {
			if _, err := strconv.ParseUint(e.Device, 10, 31); err != nil {
				return fmt.Errorf("FORGE_VIDEO_DEVICE must be a nonnegative GPU index for nvenc")
			}
		}
	case VideoEncoderVAAPI, VideoEncoderQSV:
		if e.Device != "" && !filepath.IsAbs(e.Device) {
			return fmt.Errorf("FORGE_VIDEO_DEVICE must be an absolute DRM render device path for %s", e.Backend)
		}
	default:
		return fmt.Errorf("FORGE_VIDEO_ENCODER must be cpu, nvenc, qsv, or vaapi, got %q", e.Backend)
	}
	return nil
}

func (e VideoEncoder) Hardware() bool {
	return e.Backend == VideoEncoderNVENC || e.Backend == VideoEncoderQSV || e.Backend == VideoEncoderVAAPI
}

func (e VideoEncoder) deviceArgs(acceleration VideoAcceleration) []string {
	device := firstNonEmpty(e.Device, "/dev/dri/renderD128")
	switch e.Backend {
	case VideoEncoderNVENC:
		switch acceleration {
		case VideoAccelerationCUDA:
			args := []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
			if e.Device != "" {
				args = append(args, "-hwaccel_device", e.Device)
			}
			return args
		case VideoAccelerationVulkan:
			// Derive Vulkan from CUDA so both APIs select the same physical GPU.
			return []string{"-init_hw_device", "cuda=forge_cuda:" + firstNonEmpty(e.Device, "0"),
				"-init_hw_device", "vulkan=forge_vk@forge_cuda", "-filter_hw_device", "forge_vk",
				"-hwaccel", "cuda", "-hwaccel_device", "forge_cuda", "-hwaccel_output_format", "cuda"}
		}
	case VideoEncoderVAAPI:
		return []string{"-init_hw_device", "vaapi=forge_va:" + device, "-filter_hw_device", "forge_va"}
	case VideoEncoderQSV:
		return []string{"-init_hw_device", "vaapi=forge_va:" + device,
			"-init_hw_device", "qsv=forge_qsv@forge_va", "-filter_hw_device", "forge_qsv"}
	}
	return nil
}

func hardwareVideoFrameFormat(profile VideoProfile) string {
	if profile.PixelFormat == "yuv420p10le" || profile.EncoderProfile == "main10" {
		return "p010le"
	}
	return "nv12"
}

func (e VideoEncoder) filterArgs(profile VideoProfile) []string {
	switch e.Backend {
	case VideoEncoderVAAPI:
		return []string{"format=" + hardwareVideoFrameFormat(profile), "hwupload"}
	case VideoEncoderQSV:
		return []string{"format=" + hardwareVideoFrameFormat(profile), "hwupload=extra_hw_frames=64"}
	}
	return nil
}

func (e VideoEncoder) hardwareOutputArgs(codec string, profile VideoProfile, acceleration VideoAcceleration) []string {
	args := []string{"-c:v", codec + "_" + e.Backend}
	switch e.Backend {
	case VideoEncoderNVENC:
		args = append(args, "-preset", "p4", "-rc", "vbr")
		if acceleration == VideoAccelerationCUDA {
			// NVENC uses the device associated with its input CUDA frames.
			return append(args, "-pix_fmt", "cuda")
		}
		args = append(args, "-pix_fmt", hardwareVideoFrameFormat(profile))
		if acceleration == VideoAccelerationVulkan || e.Device != "" {
			args = append(args, "-gpu", firstNonEmpty(e.Device, "0"))
		}
	case VideoEncoderQSV:
		args = append(args, "-preset", "medium")
	case VideoEncoderVAAPI:
		args = append(args, "-rc_mode", "VBR")
	}
	return args
}

func (e VideoEncoder) hardwareGOPArgs(output VideoGenerateOutput) []string {
	frameRate := output.GOPFrameRate
	if frameRate <= 0 {
		frameRate = output.Profile.FrameRate
	}
	args := []string{"-flags", "+cgop"}
	if frames := gopFrameCount(output.GOPSeconds, frameRate); frames > 0 {
		args = append(args, "-g", strconv.Itoa(frames))
	}
	switch e.Backend {
	case VideoEncoderNVENC:
		args = append(args, "-forced-idr", "1", "-no-scenecut", "1")
	case VideoEncoderQSV:
		args = append(args, "-forced_idr", "1", "-adaptive_i", "0", "-idr_interval", "0")
	case VideoEncoderVAAPI:
		args = append(args, "-idr_interval", "0")
	}
	return args
}

func BuildVideoEncoderCheckArgs(encoder VideoEncoder, profile VideoProfile) ([]string, error) {
	profile.Width, profile.Height, profile.FrameRate = 256, 144, 30
	return buildVideoGenerateArgs(VideoGenerateSpec{
		Input: "testsrc2=size=256x144:rate=30", Encoder: encoder,
		Videos: []VideoGenerateOutput{{Output: "-", Profile: profile, GOPSeconds: 2}},
	}, []string{"-loglevel", "error", "-abort_on", "empty_output", "-f", "lavfi"}, []string{"-frames:v", "4", "-f", "null"})
}

func BuildAcceleratedVideoCheckArgs(spec VideoGenerateSpec) ([]string, error) {
	if len(spec.Videos) != 1 {
		return nil, fmt.Errorf("accelerated video check requires exactly one output")
	}
	output := spec.Videos[0]
	output.Output = "-"
	spec.Videos = []VideoGenerateOutput{output}
	return buildVideoGenerateArgs(spec, []string{"-loglevel", "error", "-abort_on", "empty_output"}, []string{"-frames:v", "4", "-f", "null"})
}
