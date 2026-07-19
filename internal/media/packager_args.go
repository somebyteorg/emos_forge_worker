package media

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"forge_worker/internal/encryption"
)

type PackageTrack struct {
	TrackID         string
	Kind            string
	VideoID         string
	Input           string
	Stream          string
	Language        string
	InitSegment     string
	SegmentTemplate string
	PlaylistName    string
	HLSGroupID      string
	HLSName         string
	KeyIDHex        string
	KeyHex          string
}

type PackageSpec struct {
	Tracks          []PackageTrack
	Keys            encryption.File
	EncryptionMode  string
	HLSMaster       string
	SegmentDuration time.Duration
	DefaultLanguage string
}

const (
	PackageEncryptionNone     = "none"
	PackageEncryptionClearKey = "clearkey"
)

func BuildPackagerArgs(spec PackageSpec) ([]string, error) {
	if len(spec.Tracks) == 0 || spec.HLSMaster == "" {
		return nil, fmt.Errorf("package tracks and HLS master are required")
	}
	encryptionMode := packageEncryptionMode(spec)
	if encryptionMode != PackageEncryptionNone && encryptionMode != PackageEncryptionClearKey {
		return nil, fmt.Errorf("unsupported package encryption mode %q", spec.EncryptionMode)
	}
	keyByTrack := make(map[string]encryption.Track, len(spec.Keys.Tracks))
	for _, key := range spec.Keys.Tracks {
		keyByTrack[key.TrackID] = key
	}
	args := make([]string, 0, len(spec.Tracks)+10)
	keyArgs := make([]string, 0, len(spec.Tracks))
	for _, track := range spec.Tracks {
		if track.TrackID == "" || track.Input == "" || track.Stream == "" || track.InitSegment == "" || track.SegmentTemplate == "" || track.PlaylistName == "" {
			return nil, fmt.Errorf("package track is incomplete")
		}
		args = append(args, packagerStreamArg(track, encryptionMode == PackageEncryptionClearKey))
		if encryptionMode == PackageEncryptionClearKey {
			key := encryption.Track{TrackID: track.TrackID, Kind: track.Kind, KeyIDHex: track.KeyIDHex, KeyHex: track.KeyHex}
			if key.KeyIDHex == "" || key.KeyHex == "" {
				stored, ok := keyByTrack[track.TrackID]
				if !ok {
					return nil, fmt.Errorf("missing encryption key for track %q", track.TrackID)
				}
				key = stored
			}
			keyArgs = append(keyArgs, fmt.Sprintf("label=%s:key_id=%s:key=%s", shakaLabel(track), key.KeyIDHex, key.KeyHex))
		}
	}
	if encryptionMode == PackageEncryptionClearKey {
		args = append(args,
			"--enable_raw_key_encryption",
			"--protection_scheme", "cbcs",
			"--protection_systems", "CommonSystem",
			"--clear_lead", "0",
			"--keys", strings.Join(keyArgs, ","),
		)
	}
	if spec.SegmentDuration > 0 {
		args = append(args, "--segment_duration", strconv.FormatFloat(spec.SegmentDuration.Seconds(), 'f', -1, 64))
	}
	if spec.DefaultLanguage != "" {
		args = append(args, "--default_language", spec.DefaultLanguage)
	}
	args = append(args,
		"--hls_master_playlist_output", spec.HLSMaster,
	)
	return args, nil
}

func packageEncryptionMode(spec PackageSpec) string {
	if spec.EncryptionMode != "" {
		return strings.ToLower(strings.TrimSpace(spec.EncryptionMode))
	}
	if len(spec.Keys.Tracks) > 0 {
		return PackageEncryptionClearKey
	}
	for _, track := range spec.Tracks {
		if track.KeyIDHex != "" || track.KeyHex != "" {
			return PackageEncryptionClearKey
		}
	}
	return PackageEncryptionNone
}

func packagerStreamArg(track PackageTrack, encrypted bool) string {
	parts := []string{
		"in=" + track.Input,
		"stream=" + track.Stream,
		"init_segment=" + track.InitSegment,
		"segment_template=" + track.SegmentTemplate,
		"playlist_name=" + track.PlaylistName,
	}
	if encrypted {
		parts = append(parts, "drm_label="+shakaLabel(track))
	}
	if track.HLSGroupID != "" {
		parts = append(parts, "hls_group_id="+track.HLSGroupID)
	}
	if track.HLSName != "" {
		parts = append(parts, "hls_name="+track.HLSName)
	}
	if track.Language != "" {
		parts = append(parts, "language="+track.Language)
	}
	return strings.Join(parts, ",")
}

func shakaLabel(track PackageTrack) string {
	label := track.TrackID
	label = strings.NewReplacer(" ", "_", ".", "_", "/", "_", "\\", "_").Replace(label)
	if label == "" {
		return track.Stream
	}
	return label
}
