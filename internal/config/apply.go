package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func apply(c *Config, values map[string]string) error {
	stringValue(values, "EMOS_URL", &c.EMOSURL)
	stringValue(values, "EMOS_TOKEN", &c.EMOSToken)
	stringValue(values, "EMOS_FORGE_WORKER_ID", &c.EMOSForgeWorkerID)
	stringValue(values, "FORGE_OUTPUT_DIR", &c.OutputDir)
	stringValue(values, "FORGE_FFMPEG_PATH", &c.FFmpegPath)
	stringValue(values, "FORGE_FFPROBE_PATH", &c.FFprobePath)
	stringValue(values, "FORGE_PACKAGER_PATH", &c.PackagerPath)
	stringValue(values, "FORGE_VIPS_PATH", &c.VIPSPath)
	stringValue(values, "FORGE_ENCRYPTION_MODE", &c.EncryptionMode)
	stringValue(values, "FORGE_SPRITE_FRAME_FORMAT", &c.SpriteFrameFormat)
	c.EncryptionMode = strings.ToLower(strings.TrimSpace(c.EncryptionMode))
	c.SpriteFrameFormat = strings.ToLower(strings.TrimSpace(c.SpriteFrameFormat))

	for key, target := range map[string]*int{
		"FORGE_CPU_LIMIT":           &c.CPULimit,
		"FORGE_STEP_RETRY_MAX":      &c.StepRetryMax,
		"FORGE_AUDIO_MAX_CHANNELS":  &c.AudioMaxChannels,
		"FORGE_SPRITE_COLUMNS":      &c.SpriteColumns,
		"FORGE_SPRITE_ROWS":         &c.SpriteRows,
		"FORGE_SPRITE_AVIF_QUALITY": &c.SpriteAVIFQuality,
		"FORGE_SPRITE_AVIF_EFFORT":  &c.SpriteAVIFEffort,
		"EMOS_UPLOAD_CONCURRENCY":   &c.UploadConcurrency,
		"EMOS_UPLOAD_RETRY_MAX":     &c.UploadRetryMax,
	} {
		if err := parseInt(values, key, target); err != nil {
			return err
		}
	}
	if err := parseBool(values, "EMOS_UPLOAD_DELETE_ARTIFACTS", &c.UploadDeleteArtifacts); err != nil {
		return err
	}
	if err := parseBool(values, "EMOS_UPLOAD_TITLED_SUBTITLES_ONLY", &c.UploadTitledSubtitlesOnly); err != nil {
		return err
	}
	for key, target := range map[string]*time.Duration{
		"FORGE_POLL_IDLE_INITIAL":        &c.PollIdleInitial,
		"FORGE_POLL_MAX":                 &c.PollMax,
		"FORGE_HEARTBEAT_INTERVAL":       &c.HeartbeatInterval,
		"FORGE_HTTP_TIMEOUT":             &c.HTTPTimeout,
		"FORGE_DOWNLOAD_CONNECT_TIMEOUT": &c.DownloadConnectTimeout,
		"FORGE_RETRY_INITIAL":            &c.RetryInitial,
		"FORGE_RETRY_MAX":                &c.RetryMax,
		"FORGE_SEGMENT_TARGET":           &c.SegmentTarget,
		"FORGE_SEGMENT_MAX":              &c.SegmentMax,
	} {
		if err := parseDuration(values, key, target); err != nil {
			return err
		}
	}
	if err := parseInt64(values, "EMOS_UPLOAD_CHUNK_SIZE_BYTES", &c.UploadChunkSizeBytes); err != nil {
		return err
	}
	return nil
}

func stringValue(values map[string]string, key string, target *string) {
	if value, ok := values[key]; ok {
		*target = value
	}
}

func parseInt(values map[string]string, key string, target *int) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*target = value
	return nil
}

func parseInt64(values map[string]string, key string, target *int64) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*target = value
	return nil
}

func parseBool(values map[string]string, key string, target *bool) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*target = value
	return nil
}

func parseDuration(values map[string]string, key string, target *time.Duration) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*target = value
	return nil
}
