package media

import (
	"strconv"
	"strings"
)

func parseFrameRate(value string) float64 {
	if value == "" || value == "0/0" {
		return 0
	}
	left, right, ok := strings.Cut(value, "/")
	if !ok {
		return parseFloat(value)
	}
	numerator := parseFloat(left)
	denominator := parseFloat(right)
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func disposition(stream ffprobeStream, key string) bool {
	return stream.Disposition != nil && stream.Disposition[key] != 0
}

func tag(stream ffprobeStream, key string) string {
	for tagKey, value := range stream.Tags {
		if strings.EqualFold(tagKey, key) {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}
