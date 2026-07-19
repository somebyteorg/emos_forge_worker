package media

import (
	"slices"
	"strings"
	"testing"
	"time"

	"forge_worker/internal/encryption"
)

func TestBuildPackagerArgs(t *testing.T) {
	args, err := BuildPackagerArgs(PackageSpec{
		Tracks: []PackageTrack{
			{TrackID: "video.1080p", Kind: "video", Input: "tmp/video_1080p.mp4", Stream: "video", InitSegment: "video/1080p/init.mp4", SegmentTemplate: "video/1080p/$Number%05d$.m4s", PlaylistName: "video/1080p/main.m3u8"},
			{TrackID: "audio.eng", Kind: "audio", Input: "tmp/audio_eng.m4a", Stream: "audio", Language: "eng", InitSegment: "audio/eng/init.mp4", SegmentTemplate: "audio/eng/$Number%05d$.m4s", PlaylistName: "audio/eng/main.m3u8", HLSGroupID: "audio", HLSName: "eng"},
		},
		Keys: encryption.File{Tracks: []encryption.Track{
			{TrackID: "video.1080p", Kind: "video", KeyIDHex: "00112233445566778899aabbccddeeff", KeyHex: "11112222333344445555666677778888"},
			{TrackID: "audio.eng", Kind: "audio", KeyIDHex: "ffeeddccbbaa99887766554433221100", KeyHex: "88887777666655554444333322221111"},
		}},
		EncryptionMode:  PackageEncryptionClearKey,
		HLSMaster:       "master.m3u8",
		SegmentDuration: 10 * time.Second,
		DefaultLanguage: "eng",
	})
	if err != nil {
		t.Fatalf("BuildPackagerArgs: %v", err)
	}
	for _, item := range []string{"--enable_raw_key_encryption", "--protection_scheme", "cbcs", "--protection_systems", "CommonSystem", "--segment_duration", "10", "--default_language", "eng", "--hls_master_playlist_output", "master.m3u8"} {
		if !slices.Contains(args, item) {
			t.Fatalf("expected %q in args %#v", item, args)
		}
	}
	joined := strings.Join(args, " ")
	for _, value := range []string{"stream=video", "stream=audio", "language=eng", "label=video_1080p:key_id=00112233445566778899aabbccddeeff", "label=audio_eng:key_id=ffeeddccbbaa99887766554433221100"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("expected %q in args %s", value, joined)
		}
	}
	if strings.Contains(joined, "--mpd_output") || strings.Contains(joined, "manifest.mpd") {
		t.Fatalf("did not expect DASH output in args %s", joined)
	}
}

func TestBuildPackagerArgsWithoutEncryption(t *testing.T) {
	args, err := BuildPackagerArgs(PackageSpec{
		Tracks: []PackageTrack{
			{TrackID: "video_package", Kind: "video", Input: "tmp/video_package.mp4", Stream: "video", InitSegment: "video_package/init.mp4", SegmentTemplate: "video_package/$Number%05d$.m4s", PlaylistName: "video_package/main.m3u8"},
			{TrackID: "audio.eng", Kind: "audio", Input: "tmp/audio_eng.m4a", Stream: "audio", Language: "eng", InitSegment: "audio/eng/init.mp4", SegmentTemplate: "audio/eng/$Number%05d$.m4s", PlaylistName: "audio/eng/main.m3u8", HLSGroupID: "audio", HLSName: "eng"},
		},
		EncryptionMode:  PackageEncryptionNone,
		HLSMaster:       "master.m3u8",
		SegmentDuration: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("BuildPackagerArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, value := range []string{"--enable_raw_key_encryption", "--keys", "drm_label="} {
		if strings.Contains(joined, value) {
			t.Fatalf("did not expect %q in args %s", value, joined)
		}
	}
	for _, item := range []string{"--segment_duration", "10", "--hls_master_playlist_output", "master.m3u8"} {
		if !slices.Contains(args, item) {
			t.Fatalf("expected %q in args %#v", item, args)
		}
	}
	if strings.Contains(joined, "--mpd_output") || strings.Contains(joined, "manifest.mpd") {
		t.Fatalf("did not expect DASH output in args %s", joined)
	}
}

func TestBuildPackagerArgsRequiresKeys(t *testing.T) {
	_, err := BuildPackagerArgs(PackageSpec{
		Tracks:         []PackageTrack{{TrackID: "video.720p", Input: "tmp/video.mp4", Stream: "video", InitSegment: "video/init.mp4", SegmentTemplate: "video/segment_$Number$.m4s", PlaylistName: "video/main.m3u8"}},
		EncryptionMode: PackageEncryptionClearKey,
		HLSMaster:      "master.m3u8",
	})
	if err == nil {
		t.Fatalf("expected missing key to fail")
	}
}
