package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"forge_worker/internal/runner"
	"forge_worker/internal/task"
)

func BuildMetadataProbeArgs(input string) ([]string, error) {
	if input == "" {
		return nil, fmt.Errorf("metadata probe input is required")
	}
	return []string{"-allowed_extensions", "ALL", "-show_format", "-show_streams", "-print_format", "json", input}, nil
}

func RunMetadataProbeWithRunner(ctx context.Context, commandRunner CommandRunner, ffprobePath, input string) (map[string]any, error) {
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	args, err := BuildMetadataProbeArgs(input)
	if err != nil {
		return nil, err
	}
	result, err := commandRunner.Run(ctx, runner.Spec{Name: ffprobePath, Args: args, GracePeriod: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("ffprobe metadata probe failed: %s", conciseProbeError(result.Stderr, err))
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(result.Stdout)))
	decoder.UseNumber()
	var metadata map[string]any
	if err := decoder.Decode(&metadata); err != nil {
		return nil, fmt.Errorf("ffprobe metadata JSON could not be parsed: %w", err)
	}
	return metadata, nil
}

func RunProbeWithRunner(ctx context.Context, commandRunner CommandRunner, ffprobePath, input string) (Probe, error) {
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	result, err := commandRunner.Run(ctx, runner.Spec{
		Name:        ffprobePath,
		Args:        []string{"-v", "error", "-show_format", "-show_streams", "-print_format", "json", input},
		GracePeriod: 2 * time.Second,
	})
	if err != nil {
		return Probe{}, task.NewError(task.ErrProbeFailed, conciseProbeError(result.Stderr, err), true)
	}
	return ParseProbe([]byte(result.Stdout))
}

func conciseProbeError(stderr string, err error) string {
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return fmt.Sprintf("ffprobe failed: %v", err)
}
