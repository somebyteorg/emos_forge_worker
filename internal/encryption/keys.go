package encryption

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

type File struct {
	SchemaVersion int     `json:"schema_version"`
	TaskUUID      string  `json:"task_uuid"`
	Scheme        string  `json:"scheme"`
	Tracks        []Track `json:"tracks"`
	CreatedAt     string  `json:"created_at"`
}

type Track struct {
	TrackID  string `json:"track_id"`
	Kind     string `json:"kind"`
	VideoID  string `json:"video_id,omitempty"`
	KeyIDHex string `json:"key_id_hex"`
	KeyHex   string `json:"key_hex"`
}

type TrackSpec struct {
	TrackID string
	Kind    string
	VideoID string
}

func Generate(taskUUID string, tracks []TrackSpec, now time.Time) (File, error) {
	result := File{SchemaVersion: 1, TaskUUID: taskUUID, Scheme: "cbcs", CreatedAt: now.UTC().Format(time.RFC3339)}
	trackSpecs := append([]TrackSpec(nil), tracks...)
	sort.Slice(trackSpecs, func(i, j int) bool { return trackSpecs[i].TrackID < trackSpecs[j].TrackID })
	for _, spec := range trackSpecs {
		track, err := generateTrack(spec)
		if err != nil {
			return File{}, err
		}
		result.Tracks = append(result.Tracks, track)
	}
	return result, nil
}

func generateTrack(spec TrackSpec) (Track, error) {
	keyID := make([]byte, 16)
	key := make([]byte, 16)
	if _, err := rand.Read(keyID); err != nil {
		return Track{}, fmt.Errorf("generate key ID: %w", err)
	}
	if _, err := rand.Read(key); err != nil {
		return Track{}, fmt.Errorf("generate content key: %w", err)
	}
	return Track{
		TrackID: spec.TrackID, Kind: spec.Kind, VideoID: spec.VideoID,
		KeyIDHex: hex.EncodeToString(keyID), KeyHex: hex.EncodeToString(key),
	}, nil
}
