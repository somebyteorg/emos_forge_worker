package pipeline

import (
	"forge_worker/internal/media"
)

func dolbyVisionStreamDetails(stream media.VideoStream) map[string]any {
	return map[string]any{
		"dolby_vision":                   true,
		"source_track_index":             stream.Index,
		"codec":                          stream.Codec,
		"dynamic_range":                  stream.DynamicRange,
		"dolby_vision_profile":           stream.DolbyVisionProfile,
		"dolby_vision_level":             stream.DolbyVisionLevel,
		"dolby_vision_base_layer":        stream.DolbyVisionBaseLayer,
		"dolby_vision_enhancement_layer": stream.DolbyVisionEnhancementLayer,
		"dolby_vision_rpu":               stream.DolbyVisionRPU,
		"dolby_vision_compatibility_id":  stream.DolbyVisionCompatibilityID,
		"dolby_vision_hdr10_compatible":  stream.DolbyVisionHDR10Compatible,
	}
}
