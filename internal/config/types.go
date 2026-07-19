package config

import "time"

type Config struct {
	EMOSURL                   string
	EMOSToken                 string
	EMOSForgeWorkerID         string
	OutputDir                 string
	CPULimit                  int
	PollIdleInitial           time.Duration
	PollMax                   time.Duration
	HeartbeatInterval         time.Duration
	HTTPTimeout               time.Duration
	DownloadConnectTimeout    time.Duration
	StepRetryMax              int
	RetryInitial              time.Duration
	RetryMax                  time.Duration
	FFmpegPath                string
	FFprobePath               string
	PackagerPath              string
	VIPSPath                  string
	AudioMaxChannels          int
	EncryptionMode            string
	SegmentTarget             time.Duration
	SegmentMax                time.Duration
	SpriteColumns             int
	SpriteRows                int
	SpriteAVIFQuality         int
	SpriteAVIFEffort          int
	SpriteFrameFormat         string
	UploadConcurrency         int
	UploadRetryMax            int
	UploadChunkSizeBytes      int64
	UploadDeleteArtifacts     bool
	UploadTitledSubtitlesOnly bool
}
