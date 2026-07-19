package pipeline

import (
	"forge_worker/internal/media"
)

func dolbyVisionProbeDetails(probe media.Probe) (map[string]any, bool) {
	indices := make([]int, 0, len(probe.VideoStreams))
	ranges := make([]media.DynamicRange, 0, len(probe.VideoStreams))
	for _, stream := range probe.VideoStreams {
		if stream.DolbyVision || stream.DynamicRange == media.DynamicRangeDolby {
			indices = append(indices, stream.Index)
			ranges = append(ranges, stream.DynamicRange)
		}
	}
	if !probe.DolbyVision && len(indices) == 0 {
		return nil, false
	}
	return map[string]any{
		"dolby_vision":         true,
		"video_stream_indices": indices,
		"dynamic_ranges":       ranges,
	}, true
}
