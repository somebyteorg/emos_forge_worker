package pipeline

import (
	"fmt"
	"sort"

	"forge_worker/internal/encryption"
	"forge_worker/internal/state"
)

func keysFromPackagedArtifacts(taskUUID string, artifacts []state.ArtifactRecord) (encryption.File, error) {
	keys := encryption.File{SchemaVersion: 1, TaskUUID: taskUUID, Scheme: "cbcs"}
	seen := make(map[string]bool)
	for _, artifact := range artifacts {
		if !artifact.Committed || (artifact.Kind != "video_packaged" && artifact.Kind != "audio_packaged") {
			continue
		}
		metadata, err := packagedTrackMetadataFromArtifact(artifact)
		if err != nil {
			return encryption.File{}, err
		}
		if metadata.KeyIDHex == "" || metadata.KeyHex == "" {
			return encryption.File{}, fmt.Errorf("packaged track %s has no key metadata", metadata.ID)
		}
		if seen[metadata.ID] {
			continue
		}
		seen[metadata.ID] = true
		keys.Tracks = append(keys.Tracks, encryption.Track{TrackID: metadata.ID, Kind: metadata.Kind, VideoID: metadata.VideoID, KeyIDHex: metadata.KeyIDHex, KeyHex: metadata.KeyHex})
	}
	sort.Slice(keys.Tracks, func(i, j int) bool { return keys.Tracks[i].TrackID < keys.Tracks[j].TrackID })
	return keys, nil
}
