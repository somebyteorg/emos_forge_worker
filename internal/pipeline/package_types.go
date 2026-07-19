package pipeline

import (
	"time"

	"forge_worker/internal/media"
)

type packagePlan struct {
	Tracks               []media.PackageTrack
	Artifacts            []packagedArtifactSpec
	SegmentDuration      time.Duration
	DefaultLanguage      string
	DefaultAudioPlaylist string
}

type packagedArtifactSpec struct {
	Kind         string
	RelativePath string
	Metadata     any
}

type packagedTrackMetadata struct {
	ID                     string               `json:"id"`
	Kind                   string               `json:"kind"`
	VideoID                string               `json:"video_id,omitempty"`
	Profile                media.VideoProfile   `json:"profile,omitempty"`
	Audio                  media.AudioSelection `json:"audio,omitempty"`
	InitPath               string               `json:"init_path"`
	PlaylistPath           string               `json:"playlist_path"`
	SegmentPattern         string               `json:"segment_pattern,omitempty"`
	SegmentDurationSeconds float64              `json:"segment_duration_seconds,omitempty"`
	KeyIDHex               string               `json:"key_id_hex,omitempty"`
	KeyHex                 string               `json:"key_hex,omitempty"`
}

type videoIntermediateMetadata struct {
	SourceIndex            int                `json:"source_index"`
	Profile                media.VideoProfile `json:"profile"`
	Mode                   string             `json:"mode,omitempty"`
	InputMode              string             `json:"input_mode,omitempty"`
	GOPSeconds             float64            `json:"gop_seconds,omitempty"`
	SegmentDurationSeconds float64            `json:"segment_duration_seconds,omitempty"`
}

type segmentMetadata struct {
	TrackID string `json:"track_id"`
	Kind    string `json:"kind"`
}
