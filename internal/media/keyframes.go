package media

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"forge_worker/internal/runner"
)

type Keyframe struct {
	Index       int
	StreamIndex int
	Timestamp   float64
}

type ffprobeFrameDocument struct {
	Frames []ffprobeFrame `json:"frames"`
}

type ffprobeFrame struct {
	StreamIndex             int    `json:"stream_index"`
	KeyFrame                int    `json:"key_frame"`
	BestEffortTimestampTime string `json:"best_effort_timestamp_time"`
	PacketPTSTime           string `json:"pkt_pts_time"`
	PacketDTSTime           string `json:"pkt_dts_time"`
}

func RunVideoKeyframesWithRunner(ctx context.Context, commandRunner CommandRunner, ffprobePath, input string, streamIndex int, readDuration time.Duration) ([]Keyframe, error) {
	if input == "" || streamIndex < 0 {
		return nil, fmt.Errorf("keyframe probe input and stream index are required")
	}
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	args := []string{"-v", "error", "-select_streams", "v"}
	if readDuration > 0 {
		args = append(args, "-read_intervals", "%+"+strconv.FormatFloat(readDuration.Seconds(), 'f', -1, 64))
	}
	args = append(args,
		"-skip_frame", "nokey",
		"-show_entries", "frame=stream_index,key_frame,best_effort_timestamp_time,pkt_pts_time,pkt_dts_time",
		"-of", "json",
		input,
	)
	result, err := commandRunner.Run(ctx, runner.Spec{Name: ffprobePath, Args: args, GracePeriod: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("ffprobe keyframe probe failed: %w", err)
	}
	var document ffprobeFrameDocument
	if err := json.Unmarshal([]byte(result.Stdout), &document); err != nil {
		return nil, fmt.Errorf("ffprobe keyframe JSON could not be parsed: %w", err)
	}
	keyframes := make([]Keyframe, 0, len(document.Frames))
	streamPositions := make(map[int]int)
	for _, frame := range document.Frames {
		if frame.KeyFrame == 0 {
			continue
		}
		index := streamPositions[frame.StreamIndex]
		streamPositions[frame.StreamIndex] = index + 1
		if frame.StreamIndex != streamIndex {
			continue
		}
		timestamp := firstTimestamp(frame.BestEffortTimestampTime, frame.PacketPTSTime, frame.PacketDTSTime)
		if timestamp < 0 {
			continue
		}
		keyframes = append(keyframes, Keyframe{Index: index, StreamIndex: frame.StreamIndex, Timestamp: timestamp})
	}
	sort.Slice(keyframes, func(i, j int) bool { return keyframes[i].Timestamp < keyframes[j].Timestamp })
	return keyframes, nil
}

func firstTimestamp(values ...string) float64 {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		timestamp := parseFloat(value)
		if timestamp >= 0 {
			return timestamp
		}
	}
	return -1
}
