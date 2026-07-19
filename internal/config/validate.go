package config

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

func (c Config) Validate() error {
	if c.CPULimit <= 0 {
		return fmt.Errorf("FORGE_CPU_LIMIT must be positive")
	}
	if c.CPULimit > runtime.NumCPU() {
		return fmt.Errorf("FORGE_CPU_LIMIT %d exceeds visible CPU count %d", c.CPULimit, runtime.NumCPU())
	}
	if c.PollIdleInitial <= 0 || c.PollMax < c.PollIdleInitial {
		return fmt.Errorf("worker poll intervals are invalid")
	}
	if c.HeartbeatInterval <= 0 || c.HTTPTimeout <= 0 || c.DownloadConnectTimeout <= 0 {
		return fmt.Errorf("network timeouts must be positive")
	}
	if c.StepRetryMax <= 0 || c.RetryInitial <= 0 || c.RetryMax < c.RetryInitial {
		return fmt.Errorf("pipeline retry settings are invalid")
	}
	for name, value := range map[string]string{
		"FORGE_FFMPEG_PATH": c.FFmpegPath, "FORGE_FFPROBE_PATH": c.FFprobePath,
		"FORGE_PACKAGER_PATH": c.PackagerPath, "FORGE_VIPS_PATH": c.VIPSPath,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", name)
		}
	}
	if c.AudioMaxChannels <= 0 {
		return fmt.Errorf("FORGE_AUDIO_MAX_CHANNELS must be positive")
	}
	if c.EncryptionMode != "none" && c.EncryptionMode != "clearkey" {
		return fmt.Errorf("FORGE_ENCRYPTION_MODE only supports none or clearkey")
	}
	if c.SegmentTarget <= 0 || c.SegmentMax < c.SegmentTarget {
		return fmt.Errorf("segment durations are invalid")
	}
	if !filepath.IsAbs(c.OutputDir) {
		return fmt.Errorf("output directory must be absolute")
	}
	if c.SpriteColumns <= 0 || c.SpriteRows <= 0 {
		return fmt.Errorf("sprite grid dimensions must be positive")
	}
	if c.SpriteAVIFQuality < 1 || c.SpriteAVIFQuality > 100 || c.SpriteAVIFEffort < 0 || c.SpriteAVIFEffort > 9 {
		return fmt.Errorf("sprite AVIF settings are invalid")
	}
	if c.SpriteFrameFormat != "png" && c.SpriteFrameFormat != "ppm" {
		return fmt.Errorf("FORGE_SPRITE_FRAME_FORMAT only supports png or ppm")
	}
	if c.UploadConcurrency <= 0 || c.UploadRetryMax <= 0 || c.UploadChunkSizeBytes <= 0 {
		return fmt.Errorf("upload settings must be positive")
	}
	return nil
}
