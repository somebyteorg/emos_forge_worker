package config

import (
	"os"
	"path/filepath"
	"time"
)

func Defaults() Config {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		cwd = "."
	}
	return Config{
		OutputDir:                 filepath.Join(cwd, "output"),
		CPULimit:                  4,
		PollIdleInitial:           30 * time.Second,
		PollMax:                   5 * time.Minute,
		HeartbeatInterval:         time.Minute,
		HTTPTimeout:               15 * time.Second,
		DownloadConnectTimeout:    30 * time.Second,
		StepRetryMax:              3,
		RetryInitial:              2 * time.Second,
		RetryMax:                  5 * time.Minute,
		FFmpegPath:                "ffmpeg",
		FFprobePath:               "ffprobe",
		PackagerPath:              "packager",
		VIPSPath:                  "vips",
		AudioMaxChannels:          6,
		EncryptionMode:            "clearkey",
		SegmentTarget:             10 * time.Second,
		SegmentMax:                10 * time.Second,
		SpriteColumns:             10,
		SpriteRows:                10,
		SpriteAVIFQuality:         70,
		SpriteAVIFEffort:          4,
		SpriteFrameFormat:         "png",
		UploadConcurrency:         10,
		UploadRetryMax:            3,
		UploadChunkSizeBytes:      100 << 20,
		UploadDeleteArtifacts:     false,
		UploadTitledSubtitlesOnly: true,
	}
}
