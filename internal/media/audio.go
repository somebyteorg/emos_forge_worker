package media

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type AudioTrack struct {
	Index          int    `json:"index"`
	Codec          string `json:"codec"`
	Profile        string `json:"profile,omitempty"`
	Language       string `json:"language"`
	Title          string `json:"title,omitempty"`
	SampleRate     int    `json:"sample_rate"`
	Channels       int    `json:"channels"`
	ChannelLayout  string `json:"channel_layout,omitempty"`
	Bitrate        int64  `json:"bitrate,omitempty"`
	Default        bool   `json:"default"`
	Commentary     bool   `json:"commentary"`
	VisualImpaired bool   `json:"visual_impaired"`
}

type AudioSelection struct {
	Source         AudioTrack `json:"source"`
	OutputCodec    string     `json:"output_codec"`
	OutputProfile  string     `json:"output_profile"`
	OutputBitrate  int64      `json:"output_bitrate"`
	OutputChannels int        `json:"output_channels"`
	Copy           bool       `json:"copy"`
}

type AudioSelectionOptions struct {
	Strategy              string
	Languages             []string
	IncludeCommentary     bool
	IncludeVisualImpaired bool
	MaxChannels           int
	CopyAll               bool
}

var whitespace = regexp.MustCompile(`\s+`)

func SelectAudioTracks(tracks []AudioTrack, options AudioSelectionOptions) ([]AudioSelection, error) {
	if options.MaxChannels <= 0 {
		options.MaxChannels = 6
	}
	allowed := []string{"one_per_language", "all_languages", "default_only", "selected_languages"}
	if !slices.Contains(allowed, options.Strategy) {
		return nil, fmt.Errorf("unknown audio selection strategy %q", options.Strategy)
	}
	wantedLanguages := make(map[string]bool)
	for _, language := range options.Languages {
		wantedLanguages[NormalizeLanguage(language)] = true
	}

	filtered := make([]AudioTrack, 0, len(tracks))
	for _, track := range tracks {
		track.Language = NormalizeLanguage(track.Language)
		if track.Commentary && !options.IncludeCommentary {
			continue
		}
		if track.VisualImpaired && !options.IncludeVisualImpaired {
			continue
		}
		if options.Strategy == "selected_languages" && !wantedLanguages[track.Language] {
			continue
		}
		if options.Strategy == "default_only" && !track.Default {
			continue
		}
		filtered = append(filtered, track)
	}

	groups := make(map[string][]AudioTrack)
	for _, track := range filtered {
		key := semanticKey(track)
		if options.Strategy == "one_per_language" || options.Strategy == "selected_languages" || options.Strategy == "default_only" {
			key = track.Language + ":" + audioRole(track)
		}
		groups[key] = append(groups[key], track)
	}

	selected := make([]AudioTrack, 0, len(groups))
	for _, group := range groups {
		sort.SliceStable(group, func(i, j int) bool { return betterAudioTrack(group[i], group[j]) })
		selected = append(selected, group[0])
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Index < selected[j].Index })

	result := make([]AudioSelection, 0, len(selected))
	for _, track := range selected {
		if options.CopyAll {
			result = append(result, AudioSelection{
				Source: track, OutputCodec: NormalizeAudioCodec(track.Codec), OutputProfile: track.Profile, OutputBitrate: track.Bitrate,
				OutputChannels: track.Channels, Copy: true,
			})
			continue
		}
		channels := min(track.Channels, options.MaxChannels)
		copyTrack := strings.EqualFold(track.Codec, "aac") && isAACLC(track.Profile) && track.Channels <= options.MaxChannels
		bitrate := DefaultAACBitrate(channels)
		if copyTrack && track.Bitrate > 0 {
			bitrate = track.Bitrate
		}
		result = append(result, AudioSelection{
			Source: track, OutputCodec: "aac", OutputProfile: "lc", OutputBitrate: bitrate,
			OutputChannels: channels, Copy: copyTrack,
		})
	}
	return result, nil
}

func NewAACAudioSelection(source AudioTrack, maxChannels int) AudioSelection {
	if maxChannels <= 0 {
		maxChannels = 6
	}
	channels := min(source.Channels, maxChannels)
	if channels <= 0 {
		channels = min(source.Channels, 2)
	}
	if channels <= 0 {
		channels = 2
	}
	return AudioSelection{
		Source: source, OutputCodec: "aac", OutputProfile: "lc", OutputBitrate: DefaultAACBitrate(channels),
		OutputChannels: channels, Copy: false,
	}
}

func CanCopyAudioToHLS(codec string) bool {
	switch NormalizeAudioCodec(codec) {
	case "aac", "ac3", "eac3":
		return true
	default:
		return false
	}
}

func DefaultAACBitrate(channels int) int64 {
	switch {
	case channels <= 1:
		return 96_000
	case channels == 2:
		return 128_000
	case channels <= 4:
		return 256_000
	default:
		return 384_000
	}
}

func NormalizeAudioCodec(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "", "_", "").Replace(value)
	switch value {
	case "eac3", "eac":
		return "eac3"
	case "ac3":
		return "ac3"
	case "truehd":
		return "truehd"
	case "dts", "dca":
		return "dts"
	case "mp3":
		return "mp3"
	case "opus":
		return "opus"
	case "flac":
		return "flac"
	case "aac":
		return "aac"
	default:
		return value
	}
}

func NormalizeLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	if value == "" || value == "unknown" {
		return "und"
	}
	return value
}

func semanticKey(track AudioTrack) string {
	return fmt.Sprintf("%s|%s|%d|%s|%t|%t|%t",
		track.Language, normalizeTitle(track.Title), track.Channels, strings.ToLower(track.ChannelLayout),
		track.Commentary, track.VisualImpaired, track.Default,
	)
}

func normalizeTitle(value string) string {
	return strings.ToLower(whitespace.ReplaceAllString(strings.TrimSpace(value), " "))
}

func audioRole(track AudioTrack) string {
	if track.Commentary {
		return "commentary"
	}
	if track.VisualImpaired {
		return "visual_impaired"
	}
	return "main"
}

func betterAudioTrack(left, right AudioTrack) bool {
	if left.Default != right.Default {
		return left.Default
	}
	if left.Channels != right.Channels {
		return left.Channels > right.Channels
	}
	if left.Bitrate != right.Bitrate {
		return left.Bitrate > right.Bitrate
	}
	return left.Index < right.Index
}

func isAACLC(profile string) bool {
	profile = strings.ToLower(strings.TrimSpace(profile))
	return profile == "lc" || profile == "aac low complexity" || profile == "low complexity"
}
