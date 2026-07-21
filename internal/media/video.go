package media

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"forge_worker/internal/task"
)

type DynamicRange string

const (
	DynamicRangeSDR       DynamicRange = "sdr"
	DynamicRangeHDR10     DynamicRange = "hdr10"
	DynamicRangeHDR10Plus DynamicRange = "hdr10+"
	DynamicRangeHLG       DynamicRange = "hlg"
	DynamicRangeDolby     DynamicRange = "dolby_vision"
)

type VideoSource struct {
	Width          int
	Height         int
	AverageBitrate int64
	DynamicRange   DynamicRange
	BitDepth       int
}

type VideoProfile struct {
	Name             string       `json:"name"`
	Codec            string       `json:"codec"`
	EncoderProfile   string       `json:"encoder_profile"`
	Width            int          `json:"width"`
	Height           int          `json:"height"`
	FrameRate        float64      `json:"frame_rate,omitempty"`
	AverageBitrate   int64        `json:"average_bitrate"`
	PeakBitrate      int64        `json:"peak_bitrate"`
	BufferSize       int64        `json:"buffer_size"`
	PixelFormat      string       `json:"pixel_format"`
	DynamicRange     DynamicRange `json:"dynamic_range"`
	BitrateEstimated bool         `json:"bitrate_estimated"`
}

func VideoStreamForProcessing(stream VideoStream) (VideoStream, bool) {
	if !stream.DolbyVision && stream.DynamicRange != DynamicRangeDolby {
		return stream, true
	}
	if !stream.DolbyVisionHDR10Compatible {
		return VideoStream{}, false
	}
	stream.DynamicRange = DynamicRangeHDR10
	return stream, true
}

type profileLimit struct {
	name       string
	width      int
	height     int
	sdrAverage int64
	sdrPeak    int64
	hdrAverage int64
	hdrPeak    int64
}

var profileLimits = []profileLimit{
	{name: "720p", width: 1280, height: 720, sdrAverage: 3_200_000, sdrPeak: 4_500_000, hdrAverage: 4_500_000, hdrPeak: 6_000_000},
	{name: "1080p", width: 1920, height: 1080, sdrAverage: 6_000_000, sdrPeak: 10_000_000, hdrAverage: 7_000_000, hdrPeak: 10_000_000},
	{name: "2160p", width: 3840, height: 2160, sdrAverage: 16_000_000, sdrPeak: 22_000_000, hdrAverage: 20_000_000, hdrPeak: 30_000_000},
}

func SelectVideoProfiles(source VideoSource, requested []string) ([]VideoProfile, error) {
	if source.Width <= 0 || source.Height <= 0 {
		return nil, task.NewError(task.ErrUnsupportedMedia, "video dimensions are invalid", false)
	}
	if source.DynamicRange == DynamicRangeDolby {
		return nil, task.NewError(task.ErrUnsupportedDolbyVision, "Dolby Vision video is not supported by this worker", false)
	}
	if len(requested) == 0 {
		requested = []string{"auto"}
	}
	if slices.Contains(requested, "auto") {
		if len(requested) != 1 {
			return nil, task.NewError(task.ErrInvalidTaskSchema, "auto cannot be combined with explicit video profiles", false)
		}
		limit := autoLimit(source)
		return []VideoProfile{buildProfile(source, limit)}, nil
	}

	seen := make(map[string]bool)
	profiles := make([]VideoProfile, 0, len(requested))
	for _, name := range requested {
		if seen[name] {
			continue
		}
		seen[name] = true
		limit, ok := findLimit(name)
		if !ok {
			return nil, task.NewError(task.ErrInvalidTaskSchema, fmt.Sprintf("unknown video profile %q", name), false)
		}
		// At least one source dimension must reach the profile boundary. This supports
		// cinemascope and portrait inputs without treating a preserved aspect ratio as upsampling.
		if source.Width < limit.width && source.Height < limit.height {
			continue
		}
		profiles = append(profiles, buildProfile(source, limit))
	}
	if len(profiles) == 0 {
		return nil, task.NewError(task.ErrNoValidVideoProfile, "no requested video profile can be produced without upsampling", false)
	}
	return profiles, nil
}

func autoLimit(source VideoSource) profileLimit {
	for _, limit := range profileLimits {
		if source.Width <= limit.width && source.Height <= limit.height {
			return limit
		}
	}
	return profileLimits[len(profileLimits)-1]
}

func findLimit(name string) (profileLimit, bool) {
	for _, limit := range profileLimits {
		if limit.name == strings.ToLower(strings.TrimSpace(name)) {
			return limit, true
		}
	}
	return profileLimit{}, false
}

func buildProfile(source VideoSource, limit profileLimit) VideoProfile {
	width, height := fitEven(source.Width, source.Height, limit.width, limit.height)
	outputRange := normalizeDynamicRange(source.DynamicRange)
	hdr := outputRange != DynamicRangeSDR
	averageCap, peak := limit.sdrAverage, limit.sdrPeak
	codec, encoderProfile, pixelFormat := "hevc", "main", "yuv420p"
	if hdr {
		averageCap, peak = limit.hdrAverage, limit.hdrPeak
		encoderProfile, pixelFormat = "main10", "yuv420p10le"
	} else if limit.name == "2160p" && source.BitDepth > 8 {
		encoderProfile, pixelFormat = "main10", "yuv420p10le"
	}
	if limit.name == "720p" {
		outputRange = DynamicRangeSDR
		averageCap, peak = limit.sdrAverage, limit.sdrPeak
		codec, encoderProfile, pixelFormat = "h264", "high", "yuv420p"
	}
	average := averageCap
	estimated := source.AverageBitrate <= 0
	if source.AverageBitrate > 0 {
		average = min(averageCap, int64(math.Round(float64(source.AverageBitrate)*0.90)))
	}
	return VideoProfile{
		Name: limit.name, Codec: codec, EncoderProfile: encoderProfile,
		Width: width, Height: height, AverageBitrate: average, PeakBitrate: peak,
		BufferSize: peak * 2, PixelFormat: pixelFormat, DynamicRange: outputRange,
		BitrateEstimated: estimated,
	}
}

func normalizeDynamicRange(value DynamicRange) DynamicRange {
	if value == "" {
		return DynamicRangeSDR
	}
	return value
}

func fitEven(sourceWidth, sourceHeight, maxWidth, maxHeight int) (int, int) {
	scale := min(1.0, min(float64(maxWidth)/float64(sourceWidth), float64(maxHeight)/float64(sourceHeight)))
	width := int(math.Floor(float64(sourceWidth)*scale)) &^ 1
	height := int(math.Floor(float64(sourceHeight)*scale)) &^ 1
	return max(width, 2), max(height, 2)
}
