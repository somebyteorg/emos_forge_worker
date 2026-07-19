package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	TaskUUID      string         `json:"task_uuid"`
	Status        string         `json:"status"`
	CreatedAt     string         `json:"created_at"`
	CompletedAt   string         `json:"completed_at,omitempty"`
	Source        map[string]any `json:"source"`
	Playback      Playback       `json:"playback"`
	VideoTracks   []VideoTrack   `json:"video_tracks"`
	AudioTracks   []AudioTrack   `json:"audio_tracks"`
	Subtitles     []Subtitle     `json:"subtitles"`
	Sprites       []SpriteSheet  `json:"sprites"`
}

type Playback struct {
	HLSMaster            string     `json:"hls_master"`
	Container            string     `json:"container"`
	SegmentFormat        string     `json:"segment_format"`
	Encryption           Encryption `json:"encryption"`
	SegmentTargetSeconds float64    `json:"segment_target_seconds"`
	SegmentMaxSeconds    float64    `json:"segment_max_seconds"`
	IndependentSegments  bool       `json:"independent_segments,omitempty"`
}

type Encryption struct {
	Scheme    string `json:"scheme"`
	KeySystem string `json:"key_system,omitempty"`
}

type VideoTrack struct {
	MediaID                 string         `json:"media_id"`
	Profile                 string         `json:"profile"`
	Codec                   string         `json:"codec"`
	CodecString             string         `json:"codec_string,omitempty"`
	Width                   int            `json:"width"`
	Height                  int            `json:"height"`
	FrameRate               float64        `json:"frame_rate"`
	AverageBitrate          int64          `json:"average_bitrate"`
	PeakBitrate             int64          `json:"peak_bitrate"`
	DynamicRange            string         `json:"dynamic_range"`
	PixelFormat             string         `json:"pixel_format"`
	InitPath                string         `json:"init_path"`
	InitSizeBytes           int64          `json:"init_size_bytes,omitempty"`
	PlaylistPath            string         `json:"playlist_path"`
	PlaylistSizeBytes       int64          `json:"playlist_size_bytes,omitempty"`
	KeyID                   string         `json:"key_id,omitempty"`
	KeyHex                  string         `json:"key_hex,omitempty"`
	VariantBandwidth        int64          `json:"variant_bandwidth,omitempty"`
	VariantAverageBandwidth int64          `json:"variant_average_bandwidth,omitempty"`
	VariantCodecs           string         `json:"variant_codecs,omitempty"`
	VariantVideoRange       string         `json:"variant_video_range,omitempty"`
	AudioGroupID            string         `json:"audio_group_id,omitempty"`
	SubtitlesGroupID        string         `json:"subtitles_group_id,omitempty"`
	ClosedCaptions          string         `json:"closed_captions,omitempty"`
	PlaylistVersion         int            `json:"playlist_version,omitempty"`
	TargetDurationSeconds   int            `json:"target_duration_seconds,omitempty"`
	PlaylistType            string         `json:"playlist_type,omitempty"`
	MediaSequence           int64          `json:"media_sequence,omitempty"`
	EndList                 bool           `json:"endlist,omitempty"`
	IVHex                   string         `json:"iv_hex,omitempty"`
	Segments                []Segment      `json:"segments,omitempty"`
	Metadata                map[string]any `json:"metadata,omitempty"`
}

type AudioTrack struct {
	MediaID               string         `json:"media_id"`
	SourceTrackIndex      int            `json:"source_track_index"`
	Codec                 string         `json:"codec"`
	Profile               string         `json:"profile"`
	Language              string         `json:"language"`
	Title                 string         `json:"title,omitempty"`
	Channels              int            `json:"channels"`
	ChannelLayout         string         `json:"channel_layout,omitempty"`
	Bitrate               int64          `json:"bitrate"`
	Default               bool           `json:"default"`
	Commentary            bool           `json:"commentary"`
	VisualImpaired        bool           `json:"visual_impaired"`
	InitPath              string         `json:"init_path"`
	InitSizeBytes         int64          `json:"init_size_bytes,omitempty"`
	PlaylistPath          string         `json:"playlist_path"`
	PlaylistSizeBytes     int64          `json:"playlist_size_bytes,omitempty"`
	KeyID                 string         `json:"key_id,omitempty"`
	KeyHex                string         `json:"key_hex,omitempty"`
	RenditionGroupID      string         `json:"rendition_group_id,omitempty"`
	RenditionName         string         `json:"rendition_name,omitempty"`
	RenditionAutoselect   bool           `json:"rendition_autoselect,omitempty"`
	PlaylistVersion       int            `json:"playlist_version,omitempty"`
	TargetDurationSeconds int            `json:"target_duration_seconds,omitempty"`
	PlaylistType          string         `json:"playlist_type,omitempty"`
	MediaSequence         int64          `json:"media_sequence,omitempty"`
	EndList               bool           `json:"endlist,omitempty"`
	IVHex                 string         `json:"iv_hex,omitempty"`
	Segments              []Segment      `json:"segments,omitempty"`
	Metadata              map[string]any `json:"metadata,omitempty"`
}

type Segment struct {
	Sequence        int64   `json:"sequence"`
	URI             string  `json:"uri"`
	Path            string  `json:"path"`
	DurationSeconds float64 `json:"duration_seconds"`
	SizeBytes       int64   `json:"size_bytes,omitempty"`
	Title           string  `json:"title,omitempty"`
	IVHex           string  `json:"iv_hex,omitempty"`
}

type Subtitle struct {
	MediaID          string `json:"media_id"`
	Path             string `json:"path"`
	SourceTrackIndex int    `json:"source_track_index"`
	Language         string `json:"language"`
	Title            string `json:"title,omitempty"`
	Default          bool   `json:"default"`
	Forced           bool   `json:"forced"`
	HearingImpaired  bool   `json:"hearing_impaired"`
	SizeBytes        int64  `json:"size_bytes,omitempty"`
}

type SpriteSheet struct {
	MediaID    string    `json:"media_id"`
	FrameTimes []float64 `json:"frame_times"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	Columns    int       `json:"columns"`
	Rows       int       `json:"rows"`
	CountFrame int       `json:"count_frame"`
	FileSize   int64     `json:"file_size"`
	Images     []string  `json:"images"`
}

type Warning struct {
	Code    string         `json:"code"`
	Step    string         `json:"step,omitempty"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported manifest schema version %d", m.SchemaVersion)
	}
	if m.TaskUUID == "" || m.Status == "" {
		return fmt.Errorf("task UUID and status are required")
	}
	for _, path := range []string{m.Playback.HLSMaster} {
		if path != "" {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("playback path: %w", err)
			}
		}
	}
	encrypted := m.Playback.Encryption.Scheme != "" && m.Playback.Encryption.Scheme != "none"
	for _, track := range m.VideoTracks {
		if track.MediaID == "" {
			return fmt.Errorf("video track has no media ID")
		}
		if encrypted && (track.KeyID == "" || track.KeyHex == "") {
			return fmt.Errorf("video track %q has incomplete key metadata", track.MediaID)
		}
		for _, path := range []string{track.InitPath, track.PlaylistPath} {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("video track %q: %w", track.MediaID, err)
			}
		}
		if err := validatePlaylistFields("video track "+track.MediaID, track.Segments); err != nil {
			return err
		}
	}
	for _, track := range m.AudioTracks {
		if track.MediaID == "" {
			return fmt.Errorf("audio track has no media ID")
		}
		if encrypted && (track.KeyID == "" || track.KeyHex == "") {
			return fmt.Errorf("audio track %q has incomplete key metadata", track.MediaID)
		}
		for _, path := range []string{track.InitPath, track.PlaylistPath} {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("audio track %q: %w", track.MediaID, err)
			}
		}
		if err := validatePlaylistFields("audio track "+track.MediaID, track.Segments); err != nil {
			return err
		}
	}
	for _, subtitle := range m.Subtitles {
		if subtitle.MediaID == "" {
			return fmt.Errorf("subtitle %q has no media ID", subtitle.Path)
		}
		if err := validateRelativePath(subtitle.Path); err != nil {
			return fmt.Errorf("subtitle %q: %w", subtitle.Path, err)
		}
	}
	for _, sprite := range m.Sprites {
		if sprite.MediaID == "" {
			return fmt.Errorf("sprite sheet has no media ID")
		}
		if sprite.Width <= 0 || sprite.Height <= 0 || sprite.Columns <= 0 || sprite.Rows <= 0 {
			return fmt.Errorf("sprite dimensions and grid must be positive")
		}
		if sprite.CountFrame != len(sprite.FrameTimes) {
			return fmt.Errorf("sprite count_frame does not match frame_times")
		}
		if len(sprite.Images) == 0 {
			return fmt.Errorf("sprite images are required")
		}
		for _, image := range sprite.Images {
			if err := validateRelativePath(image); err != nil {
				return fmt.Errorf("sprite image: %w", err)
			}
		}
	}
	return nil
}

func validatePlaylistFields(label string, segments []Segment) error {
	for _, segment := range segments {
		if segment.URI == "" || segment.Path == "" {
			return fmt.Errorf("%s segment URI and path are required", label)
		}
		if segment.DurationSeconds <= 0 {
			return fmt.Errorf("%s segment %q duration must be positive", label, segment.Path)
		}
		if err := validateRelativePath(segment.Path); err != nil {
			return fmt.Errorf("%s segment: %w", label, err)
		}
	}
	return nil
}

func validateRelativePath(path string) error {
	if path == "" || filepath.IsAbs(path) {
		return fmt.Errorf("path %q must be relative", path)
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes the task root", path)
	}
	return nil
}

func Write(path string, manifest Manifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	return writeJSON(path, manifest, ".manifest-*.partial")
}

func WriteLog(path string, log map[string]any) error {
	return writeJSON(path, log, ".log-*.partial")
}

func writeJSON(path string, value any, pattern string) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write output: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync output: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("commit output: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
