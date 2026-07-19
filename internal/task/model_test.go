package task

import (
	"strings"
	"testing"
)

func TestRequestValidateAllowsPackageWithTranscodedVideoProfiles(t *testing.T) {
	request := validVideoRequest([]string{"package", "720p", "1080p"})
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRequestValidateRejectsAutoWithPackage(t *testing.T) {
	request := validVideoRequest([]string{"auto", "package"})
	err := request.Validate()
	if err == nil || !strings.Contains(err.Error(), "auto cannot be combined") {
		t.Fatalf("expected auto combination validation error, got %v", err)
	}
}

func TestRequestValidateRejectsUnsupportedSpriteFrameFormat(t *testing.T) {
	request := Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8613",
		Input:    Input{Type: InputLocal, URI: "/data/source.mkv"},
		Output:   Output{Root: "/data/output"},
		Steps: StepRequests{
			Sprites: SpriteRequest{Enabled: true, Sizes: []string{"320x180"}, Columns: 10, Rows: 10, Quality: 70, Effort: 4, FrameFormat: "jpg"},
		},
	}
	err := request.Validate()
	if err == nil || !strings.Contains(err.Error(), "frame_format") {
		t.Fatalf("expected frame_format validation error, got %v", err)
	}
}

func validVideoRequest(profiles []string) Request {
	return Request{
		TaskUUID: "019f61e1-eb9d-7a90-adba-3a6f7ecc8612",
		Input:    Input{Type: InputLocal, URI: "/data/source.mkv"},
		Output:   Output{Root: "/data/output"},
		Steps: StepRequests{
			Video: VideoRequest{Enabled: true, Profiles: profiles},
		},
	}
}
