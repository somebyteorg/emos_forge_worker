package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"forge_worker/internal/media"
)

var errNoSpriteKeyframes = errors.New("no source keyframes were found for sprite generation")

func (e *Executor) spriteKeyframes(ctx context.Context, inputPath string, sourceIndex int, duration, interval float64) ([]selectedSpriteFrame, error) {
	keyframes, err := media.RunVideoKeyframesWithRunner(ctx, e.opt.ProbeRunner, e.opt.FFprobePath, inputPath, sourceIndex, 0)
	if err != nil {
		return nil, fmt.Errorf("probe sprite keyframes: %w", err)
	}
	frames := selectSpriteKeyframes(keyframes, duration, interval)
	if len(frames) == 0 {
		return nil, errNoSpriteKeyframes
	}
	return frames, nil
}

func selectSpriteKeyframes(keyframes []media.Keyframe, duration, interval float64) []selectedSpriteFrame {
	if len(keyframes) == 0 || duration <= 0 || interval <= 0 {
		return nil
	}
	frames := make([]selectedSpriteFrame, 0, int(math.Floor(duration/interval))+1)
	cursor := firstKeyframeAtOrAfter(keyframes, spriteStartAfterSeconds)
	if cursor >= len(keyframes) {
		return nil
	}
	start := keyframes[cursor].Timestamp
	for target := start; target <= duration+0.001; target += interval {
		if cursor >= len(keyframes) {
			break
		}
		best := cursor
		bestDistance := math.Abs(keyframes[best].Timestamp - target)
		for next := cursor + 1; next < len(keyframes); next++ {
			distance := math.Abs(keyframes[next].Timestamp - target)
			if distance > bestDistance {
				break
			}
			best = next
			bestDistance = distance
		}
		if keyframes[best].Timestamp > duration+interval/2 {
			break
		}
		frames = append(frames, selectedSpriteFrame{
			KeyframeOrdinal: keyframes[best].Index,
			SeekTimestamp:   keyframes[best].Timestamp,
			Timestamp:       roundSeconds(keyframes[best].Timestamp),
		})
		cursor = best + 1
	}
	return frames
}

func firstKeyframeAtOrAfter(keyframes []media.Keyframe, timestamp float64) int {
	return sort.Search(len(keyframes), func(i int) bool {
		return keyframes[i].Timestamp >= timestamp
	})
}

func relativeSpriteKeyframeOrdinals(frames []selectedSpriteFrame) []int {
	if len(frames) == 0 {
		return nil
	}
	base := frames[0].KeyframeOrdinal
	ordinals := make([]int, 0, len(frames))
	for _, frame := range frames {
		ordinals = append(ordinals, frame.KeyframeOrdinal-base)
	}
	return ordinals
}

func spriteSeekSecond(frames []selectedSpriteFrame) float64 {
	if len(frames) == 0 {
		return 0
	}
	return frames[0].SeekTimestamp
}

func spriteTimestamps(frames []selectedSpriteFrame) []float64 {
	timestamps := make([]float64, 0, len(frames))
	for _, frame := range frames {
		timestamps = append(timestamps, frame.Timestamp)
	}
	return timestamps
}
