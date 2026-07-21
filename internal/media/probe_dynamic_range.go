package media

import "strings"

type dolbyVisionConfiguration struct {
	Profile          int
	Level            int
	BaseLayer        bool
	EnhancementLayer bool
	RPU              bool
	CompatibilityID  int
}

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

func parseDolbyVisionConfiguration(stream ffprobeStream) dolbyVisionConfiguration {
	var configuration dolbyVisionConfiguration
	for _, sideData := range stream.SideDataList {
		value := strings.ToLower(sideData.SideDataType)
		if !strings.Contains(value, "dovi") && !strings.Contains(value, "dolby vision") && sideData.DVProfile == 0 {
			continue
		}
		if sideData.DVProfile > 0 {
			configuration.Profile = sideData.DVProfile
		}
		if sideData.DVLevel > 0 {
			configuration.Level = sideData.DVLevel
		}
		configuration.BaseLayer = configuration.BaseLayer || sideData.DVBLPresentFlag != 0
		configuration.EnhancementLayer = configuration.EnhancementLayer || sideData.DVELPresentFlag != 0
		configuration.RPU = configuration.RPU || sideData.DVRPUPresentFlag != 0
		if sideData.DVBLCompatibilityID != 0 {
			configuration.CompatibilityID = sideData.DVBLCompatibilityID
		}
	}
	return configuration
}

func dolbyVisionHDR10Compatible(stream ffprobeStream, configuration dolbyVisionConfiguration) bool {
	if !configuration.BaseLayer || !isPQBT2020(stream) {
		return false
	}
	switch configuration.Profile {
	case 7:
		return true
	case 8:
		return configuration.CompatibilityID == 1
	default:
		return false
	}
}

func isPQBT2020(stream ffprobeStream) bool {
	transfer := strings.ToLower(strings.TrimSpace(stream.ColorTransfer))
	primaries := strings.ToLower(strings.TrimSpace(stream.ColorPrimaries))
	return (transfer == "smpte2084" || transfer == "smpte st 2084") && primaries == "bt2020"
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
