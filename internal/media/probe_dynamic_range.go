package media

import "strings"

func classifyDynamicRange(stream ffprobeStream, dolby, hdr10Plus bool) DynamicRange {
	if dolby {
		return DynamicRangeDolby
	}
	if hdr10Plus {
		return DynamicRangeHDR10Plus
	}
	transfer := strings.ToLower(stream.ColorTransfer)
	primaries := strings.ToLower(stream.ColorPrimaries)
	if transfer == "arib-std-b67" || transfer == "arib_std_b67" {
		return DynamicRangeHLG
	}
	if transfer == "smpte2084" || transfer == "smpte st 2084" || primaries == "bt2020" || primaries == "bt2020nc" {
		return DynamicRangeHDR10
	}
	return DynamicRangeSDR
}

func isDolbyVision(stream ffprobeStream) bool {
	for _, sideData := range stream.SideDataList {
		value := strings.ToLower(sideData.SideDataType)
		if strings.Contains(value, "dovi") || strings.Contains(value, "dolby vision") || sideData.DVProfile > 0 || sideData.DVLevel > 0 {
			return true
		}
	}
	for key, value := range stream.Tags {
		joined := strings.ToLower(key + "=" + value)
		if strings.Contains(joined, "dovi") || strings.Contains(joined, "dolby vision") || strings.Contains(joined, "dvhe") || strings.Contains(joined, "dvh1") {
			return true
		}
	}
	return false
}

func hasHDR10Plus(stream ffprobeStream) bool {
	for _, sideData := range stream.SideDataList {
		value := strings.ToLower(sideData.SideDataType)
		if strings.Contains(value, "smpte2094-40") || strings.Contains(value, "dynamic hdr10+") || strings.Contains(value, "hdr10+") {
			return true
		}
	}
	return false
}

func parseBitDepth(stream ffprobeStream) int {
	if bits := int(parseInt64(stream.BitsPerRawSample)); bits > 0 {
		return bits
	}
	if strings.Contains(stream.PixelFormat, "10") {
		return 10
	}
	if strings.Contains(stream.PixelFormat, "12") {
		return 12
	}
	return 8
}
