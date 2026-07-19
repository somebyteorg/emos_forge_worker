package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"

	"forge_worker/internal/encryption"
	"forge_worker/internal/manifest"
	"forge_worker/internal/media"
	"forge_worker/internal/state"
)

func manifestVideoTrack(artifact state.ArtifactRecord, keys encryption.File, hls hlsManifestData) (manifest.VideoTrack, error) {
	metadata, err := packagedTrackMetadataFromArtifact(artifact)
	if err != nil {
		return manifest.VideoTrack{}, err
	}
	keyID := ""
	keyHex := ""
	if len(keys.Tracks) > 0 {
		key, ok := keyForTrack(keys, metadata.ID)
		if !ok {
			return manifest.VideoTrack{}, fmt.Errorf("missing key for video track %s", metadata.ID)
		}
		keyID = key.KeyIDHex
		keyHex = key.KeyHex
	}
	track := manifest.VideoTrack{
		MediaID: metadata.ID, Profile: metadata.Profile.Name, Codec: metadata.Profile.Codec, Width: metadata.Profile.Width, Height: metadata.Profile.Height,
		FrameRate: metadata.Profile.FrameRate, AverageBitrate: metadata.Profile.AverageBitrate, PeakBitrate: metadata.Profile.PeakBitrate,
		DynamicRange: string(metadata.Profile.DynamicRange), PixelFormat: metadata.Profile.PixelFormat,
		InitPath: metadata.InitPath, InitSizeBytes: artifact.SizeBytes, PlaylistPath: metadata.PlaylistPath, PlaylistSizeBytes: hls.Sizes[metadata.PlaylistPath],
		KeyID: keyID, KeyHex: keyHex,
	}
	if variant, ok := hls.Variants[metadata.PlaylistPath]; ok {
		track.VariantBandwidth = variant.Bandwidth
		track.VariantAverageBandwidth = variant.AverageBandwidth
		track.VariantCodecs = variant.Codecs
		track.VariantVideoRange = variant.VideoRange
		track.AudioGroupID = variant.AudioGroupID
		track.SubtitlesGroupID = variant.SubtitlesGroupID
		track.ClosedCaptions = variant.ClosedCaptions
		if variant.FrameRate > 0 {
			track.FrameRate = variant.FrameRate
		}
	}
	if playlist, ok := hls.MediaPlaylists[metadata.PlaylistPath]; ok {
		applyMediaPlaylistToVideoTrack(&track, playlist)
	}
	return track, nil
}

func manifestAudioTrack(artifact state.ArtifactRecord, keys encryption.File, hls hlsManifestData) (manifest.AudioTrack, error) {
	metadata, err := packagedTrackMetadataFromArtifact(artifact)
	if err != nil {
		return manifest.AudioTrack{}, err
	}
	keyID := ""
	keyHex := ""
	if len(keys.Tracks) > 0 {
		key, ok := keyForTrack(keys, metadata.ID)
		if !ok {
			return manifest.AudioTrack{}, fmt.Errorf("missing key for audio track %s", metadata.ID)
		}
		keyID = key.KeyIDHex
		keyHex = key.KeyHex
	}
	selection := metadata.Audio
	codec := firstNonEmpty(selection.OutputCodec, media.NormalizeAudioCodec(selection.Source.Codec), "aac")
	profile := selection.OutputProfile
	if profile == "" && codec == "aac" {
		profile = "lc"
	}
	track := manifest.AudioTrack{
		MediaID: metadata.ID, SourceTrackIndex: selection.Source.Index, Codec: codec, Profile: profile,
		Language: selection.Source.Language, Title: selection.Source.Title, Channels: selection.OutputChannels, ChannelLayout: selection.Source.ChannelLayout,
		Bitrate: selection.OutputBitrate, Default: selection.Source.Default, Commentary: selection.Source.Commentary, VisualImpaired: selection.Source.VisualImpaired,
		InitPath: metadata.InitPath, InitSizeBytes: artifact.SizeBytes, PlaylistPath: metadata.PlaylistPath, PlaylistSizeBytes: hls.Sizes[metadata.PlaylistPath],
		KeyID: keyID, KeyHex: keyHex,
	}
	if rendition, ok := hls.AudioRenditions[metadata.PlaylistPath]; ok {
		track.RenditionGroupID = rendition.GroupID
		track.RenditionName = rendition.Name
		track.RenditionAutoselect = rendition.Autoselect
	}
	if playlist, ok := hls.MediaPlaylists[metadata.PlaylistPath]; ok {
		applyMediaPlaylistToAudioTrack(&track, playlist)
	}
	return track, nil
}

func packagedTrackMetadataFromArtifact(artifact state.ArtifactRecord) (packagedTrackMetadata, error) {
	var metadata packagedTrackMetadata
	if err := json.Unmarshal([]byte(artifact.MetadataJSON), &metadata); err != nil {
		return packagedTrackMetadata{}, fmt.Errorf("decode packaged track metadata: %w", err)
	}
	if metadata.ID == "" || metadata.InitPath == "" || metadata.PlaylistPath == "" {
		return packagedTrackMetadata{}, fmt.Errorf("packaged artifact %s has incomplete metadata", artifact.RelativePath)
	}
	return metadata, nil
}

func keyForTrack(keys encryption.File, trackID string) (encryption.Track, bool) {
	for _, key := range keys.Tracks {
		if key.TrackID == trackID {
			return key, true
		}
	}
	return encryption.Track{}, false
}

func manifestSubtitle(artifact state.ArtifactRecord) manifest.Subtitle {
	var subtitle media.SubtitleStream
	_ = json.Unmarshal([]byte(artifact.MetadataJSON), &subtitle)
	return manifest.Subtitle{
		MediaID: subtitleMediaID(subtitle), Path: artifact.RelativePath, SourceTrackIndex: subtitle.Index, Language: subtitle.Language, Title: subtitle.Title,
		Default: subtitle.Default, Forced: subtitle.Forced, HearingImpaired: subtitle.HearingImpaired, SizeBytes: artifact.SizeBytes,
	}
}

func applyMediaPlaylistToVideoTrack(track *manifest.VideoTrack, playlist hlsMediaPlaylist) {
	track.PlaylistVersion = playlist.Version
	track.TargetDurationSeconds = playlist.TargetDurationSeconds
	track.PlaylistType = playlist.PlaylistType
	track.MediaSequence = playlist.MediaSequence
	track.EndList = playlist.EndList
	track.IVHex = playlist.Key.IVHex
	track.Segments = playlist.Segments
}

func applyMediaPlaylistToAudioTrack(track *manifest.AudioTrack, playlist hlsMediaPlaylist) {
	track.PlaylistVersion = playlist.Version
	track.TargetDurationSeconds = playlist.TargetDurationSeconds
	track.PlaylistType = playlist.PlaylistType
	track.MediaSequence = playlist.MediaSequence
	track.EndList = playlist.EndList
	track.IVHex = playlist.Key.IVHex
	track.Segments = playlist.Segments
}

func subtitleMediaID(subtitle media.SubtitleStream) string {
	language := safeFileSegment(subtitle.Language)
	if language == "" {
		language = "und"
	}
	return fmt.Sprintf("subtitle_%02d_%s", subtitle.Index, language)
}

func segmentCount(trackID string, artifacts []state.ArtifactRecord) int {
	count := 0
	for _, artifact := range artifacts {
		if !strings.HasSuffix(artifact.Kind, "_segment") {
			continue
		}
		var metadata segmentMetadata
		if err := json.Unmarshal([]byte(artifact.MetadataJSON), &metadata); err == nil && metadata.TrackID == trackID {
			count++
		}
	}
	return count
}
