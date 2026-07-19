package app

import "strings"

func manifestForUpload(manifest map[string]any, titledSubtitlesOnly bool) (map[string]any, int) {
	if !titledSubtitlesOnly {
		return manifest, 0
	}
	subtitles, ok := manifest["subtitles"].([]any)
	if !ok || len(subtitles) == 0 {
		return manifest, 0
	}
	filtered := make([]any, 0, len(subtitles))
	for _, item := range subtitles {
		subtitle, _ := item.(map[string]any)
		title, _ := subtitle["title"].(string)
		if strings.TrimSpace(title) != "" {
			filtered = append(filtered, item)
		}
	}
	skipped := len(subtitles) - len(filtered)
	if skipped == 0 {
		return manifest, 0
	}
	uploadManifest := make(map[string]any, len(manifest))
	for key, value := range manifest {
		uploadManifest[key] = value
	}
	uploadManifest["subtitles"] = filtered
	return uploadManifest, skipped
}
