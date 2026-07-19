package media

import "testing"

func TestSelectVideoProfilesUsesExpectedCodecs(t *testing.T) {
	profiles, err := SelectVideoProfiles(VideoSource{
		Width: 1920, Height: 1080, AverageBitrate: 12_000_000, DynamicRange: DynamicRangeSDR, BitDepth: 8,
	}, []string{"1080p", "720p"})
	if err != nil {
		t.Fatalf("SelectVideoProfiles: %v", err)
	}
	codecByName := make(map[string]string, len(profiles))
	for _, profile := range profiles {
		codecByName[profile.Name] = profile.Codec
	}
	if codecByName["1080p"] != "hevc" {
		t.Fatalf("1080p codec = %q, want hevc", codecByName["1080p"])
	}
	if codecByName["720p"] != "h264" {
		t.Fatalf("720p codec = %q, want h264", codecByName["720p"])
	}
}

func TestSelectVideoProfilesConvertsHDR720pToH264EightBitSDR(t *testing.T) {
	profiles, err := SelectVideoProfiles(VideoSource{
		Width: 3840, Height: 2160, AverageBitrate: 12_000_000, DynamicRange: DynamicRangeHDR10, BitDepth: 10,
	}, []string{"720p"})
	if err != nil {
		t.Fatalf("SelectVideoProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(profiles))
	}
	profile := profiles[0]
	if profile.Codec != "h264" || profile.EncoderProfile != "high" || profile.PixelFormat != "yuv420p" || profile.DynamicRange != DynamicRangeSDR {
		t.Fatalf("unexpected 720p HDR profile: %+v", profile)
	}
	if profile.AverageBitrate != 3_200_000 || profile.PeakBitrate != 4_500_000 {
		t.Fatalf("unexpected 720p HDR bitrate caps after SDR conversion: %+v", profile)
	}
}
