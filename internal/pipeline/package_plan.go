package pipeline

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func buildPackagePlan(artifacts []state.ArtifactRecord, includeVideoPackage, includeAudioPackage bool) (packagePlan, error) {
	var plan packagePlan
	var audioArtifacts []state.ArtifactRecord
	for _, artifact := range artifacts {
		if !artifact.Committed {
			continue
		}
		switch artifact.Kind {
		case "video_intermediate":
			metadata, err := videoIntermediateMetadataFromArtifact(artifact)
			if err != nil {
				return packagePlan{}, err
			}
			if metadata.Profile.Name == "package" && !includeVideoPackage {
				continue
			}
			if err := addVideoTrackToPackagePlan(&plan, artifact); err != nil {
				return packagePlan{}, err
			}
		case "audio_intermediate", "audio_aac_intermediate":
			audioArtifacts = append(audioArtifacts, artifact)
		}
	}
	selectedAudio, err := audioPackageArtifacts(audioArtifacts, includeAudioPackage)
	if err != nil {
		return packagePlan{}, err
	}
	for _, artifact := range selectedAudio {
		if err := addAudioTrackToPackagePlan(&plan, artifact); err != nil {
			return packagePlan{}, err
		}
	}
	sort.Slice(plan.Tracks, func(i, j int) bool { return plan.Tracks[i].TrackID < plan.Tracks[j].TrackID })
	sort.Slice(plan.Artifacts, func(i, j int) bool { return plan.Artifacts[i].RelativePath < plan.Artifacts[j].RelativePath })
	return plan, nil
}

func audioPackageArtifacts(artifacts []state.ArtifactRecord, includePackage bool) ([]state.ArtifactRecord, error) {
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].RelativePath < artifacts[j].RelativePath })
	aacBySource := make(map[int]bool, len(artifacts))
	for _, artifact := range artifacts {
		selection, err := audioSelectionFromArtifact(artifact)
		if err != nil {
			return nil, err
		}
		if artifact.Kind == "audio_aac_intermediate" {
			aacBySource[selection.Source.Index] = true
		}
	}
	seenTrackIDs := make(map[string]bool, len(artifacts))
	result := make([]state.ArtifactRecord, 0, len(artifacts))
	for _, artifact := range artifacts {
		selection, err := audioSelectionFromArtifact(artifact)
		if err != nil {
			return nil, err
		}
		include := false
		switch artifact.Kind {
		case "audio_aac_intermediate":
			include = true
		case "audio_intermediate":
			include = includePackage || (audioOutputCodec(selection) == "aac" && !aacBySource[selection.Source.Index])
		}
		if !include {
			continue
		}
		id := audioTrackID(selection)
		if seenTrackIDs[id] {
			continue
		}
		seenTrackIDs[id] = true
		result = append(result, artifact)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RelativePath < result[j].RelativePath })
	return result, nil
}

func addVideoTrackToPackagePlan(plan *packagePlan, artifact state.ArtifactRecord) error {
	video, err := videoIntermediateMetadataFromArtifact(artifact)
	if err != nil {
		return err
	}
	profile := video.Profile
	segmentDuration := durationFromSeconds(video.SegmentDurationSeconds)
	if segmentDuration > 0 && (plan.SegmentDuration <= 0 || profile.Name == "package") {
		plan.SegmentDuration = segmentDuration
	}
	id := "video_" + safeFileSegment(profile.Name)
	if id == "video_" {
		id = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(artifact.RelativePath), "video_"), filepath.Ext(artifact.RelativePath))
		id = "video_" + safeFileSegment(id)
	}
	videoID := videoIdentifier(profile, id)
	base := filepath.ToSlash(filepath.Join("video", videoPackageDirectory(id)))
	metadata := packagedTrackMetadata{
		ID: id, Kind: "video", VideoID: videoID, Profile: profile, InitPath: base + "/init.mp4", PlaylistPath: base + "/index.m3u8",
		SegmentPattern: base + "/%05d.m4s", SegmentDurationSeconds: video.SegmentDurationSeconds,
	}
	plan.Tracks = append(plan.Tracks, media.PackageTrack{TrackID: id, Kind: "video", VideoID: videoID, Input: artifact.RelativePath, Stream: "video", InitSegment: metadata.InitPath, SegmentTemplate: base + "/$Number%05d$.m4s", PlaylistName: metadata.PlaylistPath})
	plan.Artifacts = append(plan.Artifacts,
		packagedArtifactSpec{Kind: "video_packaged", RelativePath: metadata.InitPath, Metadata: metadata},
		packagedArtifactSpec{Kind: "video_playlist", RelativePath: metadata.PlaylistPath, Metadata: metadata},
	)
	return nil
}

func addAudioTrackToPackagePlan(plan *packagePlan, artifact state.ArtifactRecord) error {
	selection, err := audioSelectionFromArtifact(artifact)
	if err != nil {
		return err
	}
	id := audioTrackID(selection)
	base := filepath.ToSlash(filepath.Join("audio", audioPackageDirectory(id)))
	metadata := packagedTrackMetadata{ID: id, Kind: "audio", Audio: selection, InitPath: base + "/init.mp4", PlaylistPath: base + "/index.m3u8", SegmentPattern: base + "/%05d.m4s"}
	language := packagerLanguage(selection.Source.Language)
	if selection.Source.Default && plan.DefaultAudioPlaylist == "" {
		plan.DefaultAudioPlaylist = metadata.PlaylistPath
		if language != "" && plan.DefaultLanguage == "" {
			plan.DefaultLanguage = language
		}
	}
	plan.Tracks = append(plan.Tracks, media.PackageTrack{
		TrackID: id, Kind: "audio", Input: artifact.RelativePath, Stream: "audio", Language: language,
		InitSegment: metadata.InitPath, SegmentTemplate: base + "/$Number%05d$.m4s", PlaylistName: metadata.PlaylistPath,
		HLSGroupID: "audio", HLSName: audioHLSName(selection),
	})
	plan.Artifacts = append(plan.Artifacts,
		packagedArtifactSpec{Kind: "audio_packaged", RelativePath: metadata.InitPath, Metadata: metadata},
		packagedArtifactSpec{Kind: "audio_playlist", RelativePath: metadata.PlaylistPath, Metadata: metadata},
	)
	return nil
}

func videoIntermediateMetadataFromArtifact(artifact state.ArtifactRecord) (videoIntermediateMetadata, error) {
	var metadata videoIntermediateMetadata
	if err := json.Unmarshal([]byte(artifact.MetadataJSON), &metadata); err != nil {
		return videoIntermediateMetadata{}, fmt.Errorf("decode video artifact metadata: %w", err)
	}
	if metadata.Profile.Name == "" {
		return videoIntermediateMetadata{}, fmt.Errorf("video artifact %s has no profile metadata", artifact.RelativePath)
	}
	return metadata, nil
}

func audioSelectionFromArtifact(artifact state.ArtifactRecord) (media.AudioSelection, error) {
	var selection media.AudioSelection
	if err := json.Unmarshal([]byte(artifact.MetadataJSON), &selection); err != nil {
		return media.AudioSelection{}, fmt.Errorf("decode audio artifact metadata: %w", err)
	}
	if selection.Source.Index < 0 {
		return media.AudioSelection{}, fmt.Errorf("audio artifact %s has invalid source metadata", artifact.RelativePath)
	}
	return selection, nil
}

func audioTrackID(selection media.AudioSelection) string {
	language := safeFileSegment(selection.Source.Language)
	if language == "" {
		language = "und"
	}
	codec := safeFileSegment(audioOutputCodec(selection))
	if codec == "" {
		codec = "aac"
	}
	return fmt.Sprintf("audio_%02d_%s_%s", selection.Source.Index, language, codec)
}

func videoPackageDirectory(trackID string) string {
	return strings.TrimPrefix(trackID, "video_")
}

func videoIdentifier(profile media.VideoProfile, trackID string) string {
	if profile.Name != "" {
		return profile.Name
	}
	return strings.TrimPrefix(trackID, "video_")
}

func audioPackageDirectory(trackID string) string {
	return strings.TrimPrefix(trackID, "audio_")
}

func packagerLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" || strings.EqualFold(language, "und") {
		return ""
	}
	return language
}

func audioHLSName(selection media.AudioSelection) string {
	language := strings.TrimSpace(selection.Source.Language)
	if language == "" {
		language = "und"
	}
	codec := strings.TrimSpace(audioOutputCodec(selection))
	if codec == "" {
		return language
	}
	return language + " " + codec
}

func audioPackageRequested(request task.Request) bool {
	if !request.Steps.Audio.Enabled {
		return false
	}
	return request.Steps.Audio.Package || !request.Steps.Audio.AAC
}

func hasAVRequest(request task.Request) bool {
	return request.Steps.Audio.Enabled || request.Steps.Video.Enabled
}
