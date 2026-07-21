package media

import (
	"encoding/json"
	"fmt"

	"forge_worker/internal/task"
)

func ParseProbe(data []byte) (Probe, error) {
	var document ffprobeDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return Probe{}, task.NewError(task.ErrProbeFailed, fmt.Sprintf("ffprobe JSON could not be parsed: %v", err), true)
	}
	probe := Probe{Format: FormatInfo{
		Name: document.Format.FormatName, LongName: document.Format.FormatLongName,
		Duration: parseFloat(document.Format.Duration), SizeBytes: parseInt64(document.Format.Size),
		Bitrate: parseInt64(document.Format.BitRate), ProbeScore: document.Format.ProbeScore,
	}}
	for _, stream := range document.Streams {
		switch stream.CodecType {
		case "video":
			video := normalizeVideo(stream)
			probe.VideoStreams = append(probe.VideoStreams, video)
			if video.DolbyVision {
				probe.DolbyVision = true
			}
		case "audio":
			probe.AudioStreams = append(probe.AudioStreams, normalizeAudio(stream))
		case "subtitle":
			probe.Subtitles = append(probe.Subtitles, normalizeSubtitle(stream))
		}
	}
	return probe, nil
}

func normalizeVideo(stream ffprobeStream) VideoStream {
	dolby := isDolbyVision(stream)
	dolbyConfiguration := parseDolbyVisionConfiguration(stream)
	hdr10Plus := hasHDR10Plus(stream)
	dynamicRange := classifyDynamicRange(stream, dolby, hdr10Plus)
	return VideoStream{
		Index: stream.Index, Codec: stream.CodecName, Profile: stream.Profile, Level: stream.Level,
		Width: stream.Width, Height: stream.Height, SAR: stream.SampleAspectRatio, DAR: stream.DisplayAspect,
		FrameRate:      parseFrameRate(firstNonEmpty(stream.AverageFrameRate, stream.RealFrameRate)),
		AverageBitrate: parseInt64(stream.BitRate), BitDepth: parseBitDepth(stream), PixelFormat: stream.PixelFormat,
		ColorPrimaries: stream.ColorPrimaries, ColorTransfer: stream.ColorTransfer, ColorSpace: stream.ColorSpace, ColorRange: stream.ColorRange,
		DynamicRange: dynamicRange, DolbyVision: dolby,
		DolbyVisionProfile: dolbyConfiguration.Profile, DolbyVisionLevel: dolbyConfiguration.Level,
		DolbyVisionBaseLayer: dolbyConfiguration.BaseLayer, DolbyVisionEnhancementLayer: dolbyConfiguration.EnhancementLayer,
		DolbyVisionRPU: dolbyConfiguration.RPU, DolbyVisionCompatibilityID: dolbyConfiguration.CompatibilityID,
		DolbyVisionHDR10Compatible: dolbyVisionHDR10Compatible(stream, dolbyConfiguration),
		HDR10Plus:                  hdr10Plus, Default: disposition(stream, "default"),
	}
}

func normalizeAudio(stream ffprobeStream) AudioTrack {
	return AudioTrack{
		Index: stream.Index, Codec: stream.CodecName, Profile: stream.Profile, Language: NormalizeLanguage(tag(stream, "language")),
		Title: tag(stream, "title"), SampleRate: int(parseInt64(stream.SampleRate)), Channels: stream.Channels,
		ChannelLayout: stream.ChannelLayout, Bitrate: parseInt64(stream.BitRate), Default: disposition(stream, "default"),
		Commentary:     disposition(stream, "comment") || disposition(stream, "commentary") || containsFold(tag(stream, "title"), "commentary"),
		VisualImpaired: disposition(stream, "visual_impaired") || disposition(stream, "descriptions"),
	}
}

func normalizeSubtitle(stream ffprobeStream) SubtitleStream {
	return SubtitleStream{
		Index: stream.Index, Codec: stream.CodecName, Language: NormalizeLanguage(tag(stream, "language")), Title: tag(stream, "title"),
		Default: disposition(stream, "default"), Forced: disposition(stream, "forced"), HearingImpaired: disposition(stream, "hearing_impaired"),
	}
}
