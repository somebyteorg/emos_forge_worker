package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"forge_worker/internal/manifest"
	"forge_worker/internal/state"
)

type hlsManifestData struct {
	Master          hlsMasterData
	Variants        map[string]hlsVariant
	AudioRenditions map[string]hlsRendition
	MediaPlaylists  map[string]hlsMediaPlaylist
	Sizes           map[string]int64
}

type hlsMasterData struct {
	IndependentSegments bool
}

type hlsVariant struct {
	Bandwidth        int64
	AverageBandwidth int64
	Codecs           string
	Resolution       string
	FrameRate        float64
	VideoRange       string
	AudioGroupID     string
	SubtitlesGroupID string
	ClosedCaptions   string
}

type hlsRendition struct {
	GroupID    string
	Name       string
	Language   string
	Default    bool
	Autoselect bool
	Channels   string
}

type hlsMediaPlaylist struct {
	Version               int
	TargetDurationSeconds int
	PlaylistType          string
	MediaSequence         int64
	EndList               bool
	Key                   hlsKey
	Segments              []manifest.Segment
}

type hlsKey struct {
	IVHex string
}

func loadHLSManifestData(root string, artifacts []state.ArtifactRecord) (hlsManifestData, error) {
	result := hlsManifestData{
		Variants:        map[string]hlsVariant{},
		AudioRenditions: map[string]hlsRendition{},
		MediaPlaylists:  map[string]hlsMediaPlaylist{},
		Sizes:           map[string]int64{},
	}
	for _, artifact := range artifacts {
		if !artifact.Committed {
			continue
		}
		result.Sizes[artifact.RelativePath] = artifact.SizeBytes
	}
	masterPath := filepath.Join(root, "master.m3u8")
	masterData, err := os.ReadFile(masterPath)
	if err != nil {
		return hlsManifestData{}, fmt.Errorf("read HLS master playlist: %w", err)
	}
	master, variants, renditions, err := parseHLSMasterPlaylist(string(masterData), "master.m3u8")
	if err != nil {
		return hlsManifestData{}, fmt.Errorf("parse HLS master playlist: %w", err)
	}
	result.Master = master
	result.Variants = variants
	result.AudioRenditions = renditions

	for _, artifact := range artifacts {
		if !artifact.Committed || (artifact.Kind != "video_playlist" && artifact.Kind != "audio_playlist") {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(artifact.RelativePath))
		data, err := os.ReadFile(path)
		if err != nil {
			return hlsManifestData{}, fmt.Errorf("read HLS media playlist %s: %w", artifact.RelativePath, err)
		}
		playlist, err := parseHLSMediaPlaylist(string(data), artifact.RelativePath, result.Sizes)
		if err != nil {
			return hlsManifestData{}, fmt.Errorf("parse HLS media playlist %s: %w", artifact.RelativePath, err)
		}
		result.MediaPlaylists[artifact.RelativePath] = playlist
	}
	return result, nil
}

func parseHLSMasterPlaylist(data, playlistPath string) (hlsMasterData, map[string]hlsVariant, map[string]hlsRendition, error) {
	var master hlsMasterData
	variants := map[string]hlsVariant{}
	renditions := map[string]hlsRendition{}
	lines := strings.Split(data, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case line == "" || strings.HasPrefix(line, "##"):
			continue
		case line == "#EXT-X-INDEPENDENT-SEGMENTS":
			master.IndependentSegments = true
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			attrs, err := parseHLSAttributes(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
			if err != nil {
				return hlsMasterData{}, nil, nil, err
			}
			if !strings.EqualFold(attrs["TYPE"], "AUDIO") || attrs["URI"] == "" {
				continue
			}
			path := hlsURIToTaskPath(playlistPath, attrs["URI"])
			if path == "" {
				continue
			}
			renditions[path] = hlsRendition{
				GroupID: attrs["GROUP-ID"], Name: attrs["NAME"], Language: attrs["LANGUAGE"],
				Default: yesNo(attrs["DEFAULT"]), Autoselect: yesNo(attrs["AUTOSELECT"]), Channels: attrs["CHANNELS"],
			}
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			attrs, err := parseHLSAttributes(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
			if err != nil {
				return hlsMasterData{}, nil, nil, err
			}
			uri := nextHLSURI(lines, &i)
			path := hlsURIToTaskPath(playlistPath, uri)
			if path == "" {
				continue
			}
			variants[path] = hlsVariant{
				Bandwidth: parseInt64(attrs["BANDWIDTH"]), AverageBandwidth: parseInt64(attrs["AVERAGE-BANDWIDTH"]),
				Codecs: attrs["CODECS"], Resolution: attrs["RESOLUTION"], FrameRate: parseFloat(attrs["FRAME-RATE"]),
				VideoRange: attrs["VIDEO-RANGE"], AudioGroupID: attrs["AUDIO"], SubtitlesGroupID: attrs["SUBTITLES"], ClosedCaptions: attrs["CLOSED-CAPTIONS"],
			}
		}
	}
	return master, variants, renditions, nil
}

func parseHLSMediaPlaylist(data, playlistPath string, sizes map[string]int64) (hlsMediaPlaylist, error) {
	var playlist hlsMediaPlaylist
	var currentKey hlsKey
	var pendingDuration float64
	var pendingTitle string
	var hasPendingSegment bool
	sequence := int64(1)
	lines := strings.Split(data, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		switch {
		case line == "" || strings.HasPrefix(line, "##"):
			continue
		case strings.HasPrefix(line, "#EXT-X-VERSION:"):
			playlist.Version = int(parseInt64(strings.TrimPrefix(line, "#EXT-X-VERSION:")))
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			playlist.TargetDurationSeconds = int(parseInt64(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:")))
		case strings.HasPrefix(line, "#EXT-X-PLAYLIST-TYPE:"):
			playlist.PlaylistType = strings.TrimSpace(strings.TrimPrefix(line, "#EXT-X-PLAYLIST-TYPE:"))
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			playlist.MediaSequence = parseInt64(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"))
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			if _, err := parseHLSAttributes(strings.TrimPrefix(line, "#EXT-X-MAP:")); err != nil {
				return hlsMediaPlaylist{}, err
			}
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			attrs, err := parseHLSAttributes(strings.TrimPrefix(line, "#EXT-X-KEY:"))
			if err != nil {
				return hlsMediaPlaylist{}, err
			}
			currentKey = hlsKey{IVHex: normalizeHLSIV(attrs["IV"])}
			if playlist.Key == (hlsKey{}) {
				playlist.Key = currentKey
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			duration, title, err := parseEXTINF(line)
			if err != nil {
				return hlsMediaPlaylist{}, err
			}
			pendingDuration = duration
			pendingTitle = title
			hasPendingSegment = true
		case line == "#EXT-X-ENDLIST":
			playlist.EndList = true
		case strings.HasPrefix(line, "#"):
			continue
		case hasPendingSegment:
			path := hlsURIToTaskPath(playlistPath, line)
			segment := manifest.Segment{
				Sequence: sequence, URI: line, Path: path, DurationSeconds: pendingDuration, SizeBytes: sizes[path], Title: pendingTitle,
			}
			if currentKey != (hlsKey{}) && currentKey != playlist.Key {
				segment.IVHex = currentKey.IVHex
			}
			playlist.Segments = append(playlist.Segments, segment)
			sequence++
			hasPendingSegment = false
		}
	}
	return playlist, nil
}

func parseEXTINF(line string) (float64, string, error) {
	value := strings.TrimPrefix(line, "#EXTINF:")
	durationText, title, _ := strings.Cut(value, ",")
	duration, err := strconv.ParseFloat(strings.TrimSpace(durationText), 64)
	if err != nil {
		return 0, "", fmt.Errorf("parse EXTINF duration %q: %w", durationText, err)
	}
	return duration, strings.TrimSpace(title), nil
}

func nextHLSURI(lines []string, index *int) string {
	for i := *index + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		*index = i
		return line
	}
	return ""
}

func parseHLSAttributes(input string) (map[string]string, error) {
	result := map[string]string{}
	for len(input) > 0 {
		input = strings.TrimLeft(input, " \t,")
		if input == "" {
			break
		}
		eq := strings.IndexByte(input, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("invalid HLS attribute list %q", input)
		}
		key := strings.ToUpper(strings.TrimSpace(input[:eq]))
		input = input[eq+1:]
		var value string
		if strings.HasPrefix(input, `"`) {
			end := 1
			for end < len(input) && input[end] != '"' {
				end++
			}
			if end >= len(input) {
				return nil, fmt.Errorf("unterminated quoted HLS attribute %q", key)
			}
			value = input[1:end]
			input = input[end+1:]
		} else {
			end := strings.IndexByte(input, ',')
			if end < 0 {
				value = strings.TrimSpace(input)
				input = ""
			} else {
				value = strings.TrimSpace(input[:end])
				input = input[end+1:]
			}
		}
		result[key] = value
	}
	return result, nil
}

func hlsURIToTaskPath(playlistPath, uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" || filepath.IsAbs(uri) || strings.Contains(uri, "://") || strings.HasPrefix(strings.ToLower(uri), "data:") {
		return ""
	}
	base := filepath.Dir(filepath.FromSlash(playlistPath))
	if base == "." {
		base = ""
	}
	return filepath.ToSlash(filepath.Clean(filepath.Join(base, filepath.FromSlash(uri))))
}

func normalizeHLSIV(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "0x")
	value = strings.TrimPrefix(value, "0X")
	return strings.ToLower(value)
}

func yesNo(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "YES")
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed
}
