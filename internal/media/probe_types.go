package media

import (
	"context"

	"forge_worker/internal/runner"
)

type Probe struct {
	Format       FormatInfo       `json:"format"`
	VideoStreams []VideoStream    `json:"video_streams"`
	AudioStreams []AudioTrack     `json:"audio_streams"`
	Subtitles    []SubtitleStream `json:"subtitles"`
	DolbyVision  bool             `json:"dolby_vision"`
}

type FormatInfo struct {
	Name       string  `json:"name"`
	LongName   string  `json:"long_name,omitempty"`
	Duration   float64 `json:"duration_seconds,omitempty"`
	SizeBytes  int64   `json:"size_bytes,omitempty"`
	Bitrate    int64   `json:"bitrate,omitempty"`
	ProbeScore int     `json:"probe_score,omitempty"`
}

type VideoStream struct {
	Index          int          `json:"index"`
	Codec          string       `json:"codec"`
	Profile        string       `json:"profile,omitempty"`
	Level          int          `json:"level,omitempty"`
	Width          int          `json:"width"`
	Height         int          `json:"height"`
	SAR            string       `json:"sar,omitempty"`
	DAR            string       `json:"dar,omitempty"`
	FrameRate      float64      `json:"frame_rate,omitempty"`
	AverageBitrate int64        `json:"average_bitrate,omitempty"`
	BitDepth       int          `json:"bit_depth,omitempty"`
	PixelFormat    string       `json:"pixel_format,omitempty"`
	ColorPrimaries string       `json:"color_primaries,omitempty"`
	ColorTransfer  string       `json:"color_transfer,omitempty"`
	ColorSpace     string       `json:"color_space,omitempty"`
	ColorRange     string       `json:"color_range,omitempty"`
	DynamicRange   DynamicRange `json:"dynamic_range"`
	DolbyVision    bool         `json:"dolby_vision"`
	HDR10Plus      bool         `json:"hdr10_plus"`
	Default        bool         `json:"default"`
}

type SubtitleStream struct {
	Index           int    `json:"index"`
	Codec           string `json:"codec"`
	Language        string `json:"language"`
	Title           string `json:"title,omitempty"`
	Default         bool   `json:"default"`
	Forced          bool   `json:"forced"`
	HearingImpaired bool   `json:"hearing_impaired"`
}

type CommandRunner interface {
	Run(context.Context, runner.Spec) (runner.Result, error)
}

type ffprobeDocument struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeFormat struct {
	FormatName     string `json:"format_name"`
	FormatLongName string `json:"format_long_name"`
	Duration       string `json:"duration"`
	Size           string `json:"size"`
	BitRate        string `json:"bit_rate"`
	ProbeScore     int    `json:"probe_score"`
}

type ffprobeStream struct {
	Index             int               `json:"index"`
	CodecType         string            `json:"codec_type"`
	CodecName         string            `json:"codec_name"`
	Profile           string            `json:"profile"`
	Level             int               `json:"level"`
	Width             int               `json:"width"`
	Height            int               `json:"height"`
	SampleAspectRatio string            `json:"sample_aspect_ratio"`
	DisplayAspect     string            `json:"display_aspect_ratio"`
	PixelFormat       string            `json:"pix_fmt"`
	BitsPerRawSample  string            `json:"bits_per_raw_sample"`
	ColorPrimaries    string            `json:"color_primaries"`
	ColorTransfer     string            `json:"color_transfer"`
	ColorSpace        string            `json:"color_space"`
	ColorRange        string            `json:"color_range"`
	AverageFrameRate  string            `json:"avg_frame_rate"`
	RealFrameRate     string            `json:"r_frame_rate"`
	BitRate           string            `json:"bit_rate"`
	SampleRate        string            `json:"sample_rate"`
	Channels          int               `json:"channels"`
	ChannelLayout     string            `json:"channel_layout"`
	Tags              map[string]string `json:"tags"`
	Disposition       map[string]int    `json:"disposition"`
	SideDataList      []ffprobeSideData `json:"side_data_list"`
}

type ffprobeSideData struct {
	SideDataType string `json:"side_data_type"`
	DVProfile    int    `json:"dv_profile"`
	DVLevel      int    `json:"dv_level"`
}
