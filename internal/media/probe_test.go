package media

import "testing"

func TestParseProbeDetectsDolbyVision(t *testing.T) {
	probe, err := ParseProbe([]byte(`{
  "format": {"format_name":"mov,mp4", "duration":"120.5", "size":"1000", "bit_rate":"8000", "probe_score":100},
  "streams": [
    {
      "index": 0,
      "codec_type": "video",
      "codec_name": "hevc",
      "profile": "Main 10",
      "width": 3840,
      "height": 2160,
      "pix_fmt": "yuv420p10le",
      "bits_per_raw_sample": "10",
      "avg_frame_rate": "24000/1001",
      "color_primaries": "bt2020",
      "color_transfer": "smpte2084",
      "color_space": "bt2020nc",
      "disposition": {"default": 1},
      "side_data_list": [{"side_data_type":"DOVI configuration record", "dv_profile": 5, "dv_level": 6}]
    }
  ]
}`))
	if err != nil {
		t.Fatalf("ParseProbe: %v", err)
	}
	if !probe.DolbyVision || len(probe.VideoStreams) != 1 || !probe.VideoStreams[0].DolbyVision {
		t.Fatalf("Dolby Vision not detected: %+v", probe)
	}
	if probe.VideoStreams[0].DynamicRange != DynamicRangeDolby {
		t.Fatalf("dynamic range = %s", probe.VideoStreams[0].DynamicRange)
	}
	if probe.VideoStreams[0].DolbyVisionHDR10Compatible {
		t.Fatalf("profile 5 must not be treated as HDR10 compatible: %+v", probe.VideoStreams[0])
	}
	if probe.VideoStreams[0].FrameRate < 23.97 || probe.VideoStreams[0].FrameRate > 23.98 {
		t.Fatalf("frame rate = %f", probe.VideoStreams[0].FrameRate)
	}
}

func TestParseProbeDetectsDolbyVisionProfile7HDR10BaseLayer(t *testing.T) {
	probe, err := ParseProbe([]byte(`{
  "streams": [{
    "index": 0,
    "codec_type": "video",
    "codec_name": "hevc",
    "profile": "Main 10",
    "width": 3840,
    "height": 2160,
    "pix_fmt": "yuv420p10le",
    "color_primaries": "bt2020",
    "color_transfer": "smpte2084",
    "color_space": "bt2020nc",
    "side_data_list": [{
      "side_data_type": "DOVI configuration record",
      "dv_profile": 7,
      "dv_level": 6,
      "rpu_present_flag": 1,
      "el_present_flag": 1,
      "bl_present_flag": 1,
      "dv_bl_signal_compatibility_id": 6
    }]
  }]
}`))
	if err != nil {
		t.Fatalf("ParseProbe: %v", err)
	}
	video := probe.VideoStreams[0]
	if !video.DolbyVision || !video.DolbyVisionHDR10Compatible || video.DolbyVisionProfile != 7 || video.DolbyVisionLevel != 6 {
		t.Fatalf("profile 7 compatibility was not parsed: %+v", video)
	}
	if !video.DolbyVisionBaseLayer || !video.DolbyVisionEnhancementLayer || !video.DolbyVisionRPU || video.DolbyVisionCompatibilityID != 6 {
		t.Fatalf("profile 7 layer flags were not parsed: %+v", video)
	}
	processing, ok := VideoStreamForProcessing(video)
	if !ok || processing.DynamicRange != DynamicRangeHDR10 {
		t.Fatalf("profile 7 processing stream = %+v, compatible=%t", processing, ok)
	}
}

func TestParseProbeNormalizesAudioAndSubtitles(t *testing.T) {
	probe, err := ParseProbe([]byte(`{
  "format": {"format_name":"matroska,webm"},
  "streams": [
    {
      "index": 1,
      "codec_type": "audio",
      "codec_name": "aac",
      "profile": "LC",
      "sample_rate": "48000",
      "channels": 6,
      "channel_layout": "5.1",
      "bit_rate": "192000",
      "tags": {"language":"ENG", "title":"Main"},
      "disposition": {"default": 1}
    },
    {
      "index": 2,
      "codec_type": "audio",
      "codec_name": "eac3",
      "sample_rate": "48000",
      "channels": 2,
      "tags": {"title":"Director Commentary"},
      "disposition": {}
    },
    {
      "index": 3,
      "codec_type": "subtitle",
      "codec_name": "ass",
      "tags": {"language":"zh-Hant", "title":"Traditional Chinese"},
      "disposition": {"forced": 1, "hearing_impaired": 1}
    }
  ]
}`))
	if err != nil {
		t.Fatalf("ParseProbe: %v", err)
	}
	if len(probe.AudioStreams) != 2 {
		t.Fatalf("audio streams = %d", len(probe.AudioStreams))
	}
	main := probe.AudioStreams[0]
	if main.Language != "eng" || !main.Default || main.SampleRate != 48000 || main.Bitrate != 192000 {
		t.Fatalf("unexpected main audio: %+v", main)
	}
	commentary := probe.AudioStreams[1]
	if !commentary.Commentary || commentary.Language != "und" {
		t.Fatalf("unexpected commentary audio: %+v", commentary)
	}
	if len(probe.Subtitles) != 1 || probe.Subtitles[0].Language != "zh-hant" || !probe.Subtitles[0].Forced || !probe.Subtitles[0].HearingImpaired {
		t.Fatalf("unexpected subtitles: %+v", probe.Subtitles)
	}
}

func TestParseProbeClassifiesHDRVariants(t *testing.T) {
	tests := []struct {
		name string
		json string
		want DynamicRange
	}{
		{name: "hdr10", json: `{"color_transfer":"smpte2084","color_primaries":"bt2020"}`, want: DynamicRangeHDR10},
		{name: "hlg", json: `{"color_transfer":"arib-std-b67","color_primaries":"bt2020"}`, want: DynamicRangeHLG},
		{name: "hdr10plus", json: `{"color_transfer":"smpte2084","side_data_list":[{"side_data_type":"HDR Dynamic Metadata SMPTE2094-40"}]}`, want: DynamicRangeHDR10Plus},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe, err := ParseProbe([]byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,` + tt.json[1:] + `]}`))
			if err != nil {
				t.Fatalf("ParseProbe: %v", err)
			}
			if got := probe.VideoStreams[0].DynamicRange; got != tt.want {
				t.Fatalf("dynamic range = %s, want %s", got, tt.want)
			}
		})
	}
}
