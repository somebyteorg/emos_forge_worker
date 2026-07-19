package pipeline

import (
	"math"
	"time"
)

func durationFromSeconds(value float64) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value * float64(time.Second))
}

func seconds(value time.Duration) float64 {
	if value <= 0 {
		return 0
	}
	return math.Round(value.Seconds()*1000) / 1000
}

func roundSeconds(value float64) float64 {
	return math.Round(value*1000) / 1000
}
