package app

import (
	"fmt"
	"strings"

	"forge_worker/internal/emos"
)

type emosJobStepSelection struct {
	VideoProfiles   []string
	AudioPackage    bool
	AudioAAC        bool
	SubtitlePackage bool
	SpriteSizes     []string
}

func translateEMOSJobSteps(steps []emos.JobStep) (emosJobStepSelection, error) {
	var selection emosJobStepSelection
	seenProfiles := make(map[string]bool)
	seenSprites := make(map[string]bool)
	for _, rawStep := range steps {
		step := emos.JobStep(strings.TrimSpace(string(rawStep)))
		switch step {
		case emos.JobStepVideo720P:
			selection.VideoProfiles = appendUnique(selection.VideoProfiles, seenProfiles, "720p")
		case emos.JobStepVideo1080P:
			selection.VideoProfiles = appendUnique(selection.VideoProfiles, seenProfiles, "1080p")
		case emos.JobStepVideoPackage:
			selection.VideoProfiles = appendUnique(selection.VideoProfiles, seenProfiles, "package")
		case emos.JobStepAudioPackage:
			selection.AudioPackage = true
		case emos.JobStepAudioAAC:
			selection.AudioAAC = true
		case emos.JobStepSubtitlePackage:
			selection.SubtitlePackage = true
		case emos.JobStepSprite320:
			selection.SpriteSizes = appendUnique(selection.SpriteSizes, seenSprites, "320x180")
		case emos.JobStepSprite640:
			selection.SpriteSizes = appendUnique(selection.SpriteSizes, seenSprites, "640x360")
		case emos.JobStepSprite720:
			selection.SpriteSizes = appendUnique(selection.SpriteSizes, seenSprites, "1280x720")
		default:
			return emosJobStepSelection{}, fmt.Errorf("unsupported job step %q", rawStep)
		}
	}
	return selection, nil
}

func appendUnique(values []string, seen map[string]bool, value string) []string {
	if seen[value] {
		return values
	}
	seen[value] = true
	return append(values, value)
}

func formatEMOSJobSteps(steps []emos.JobStep) string {
	values := make([]string, 0, len(steps))
	for _, step := range steps {
		values = append(values, string(step))
	}
	return strings.Join(values, ",")
}
