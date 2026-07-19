package app

import (
	"slices"
	"testing"

	"forge_worker/internal/emos"
)

func TestManifestForUploadKeepsOnlyTitledSubtitles(t *testing.T) {
	manifest := map[string]any{
		"audio_tracks": []any{map[string]any{"media_id": "audio_eng"}},
		"subtitles": []any{
			map[string]any{"media_id": "subtitle_eng", "title": "SDH"},
			map[string]any{"media_id": "subtitle_zho", "title": "  "},
			map[string]any{"media_id": "subtitle_jpn"},
		},
	}

	filtered, skipped := manifestForUpload(manifest, true)
	if skipped != 2 {
		t.Fatalf("skipped = %d, want 2", skipped)
	}
	if got := emos.ManifestMediaIDs(filtered); !slices.Equal(got, []string{"audio_eng", "subtitle_eng"}) {
		t.Fatalf("filtered media IDs = %v", got)
	}
	if got := emos.ManifestMediaIDs(manifest); !slices.Equal(got, []string{"audio_eng", "subtitle_eng", "subtitle_jpn", "subtitle_zho"}) {
		t.Fatalf("source manifest was mutated: %v", got)
	}
}

func TestManifestForUploadCanKeepUntitledSubtitles(t *testing.T) {
	manifest := map[string]any{
		"subtitles": []any{map[string]any{"media_id": "subtitle_eng"}},
	}

	filtered, skipped := manifestForUpload(manifest, false)
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if got := emos.ManifestMediaIDs(filtered); !slices.Equal(got, []string{"subtitle_eng"}) {
		t.Fatalf("media IDs = %v", got)
	}
}
