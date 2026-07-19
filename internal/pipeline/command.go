package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"forge_worker/internal/runner"
	"forge_worker/internal/task"
)

func (e *Executor) runCommandWithProgress(ctx context.Context, taskUUID, stepName, name string, args []string, code task.ErrorCode, progress commandProgress) error {
	return e.runCommandWithProgressSummary(ctx, taskUUID, stepName, name, args, "", code, progress)
}

func (e *Executor) runCommandWithProgressSummary(ctx context.Context, taskUUID, stepName, name string, args []string, summary string, code task.ErrorCode, progress commandProgress) error {
	displayArgs := append([]string(nil), args...)
	runArgs := append([]string(nil), args...)
	if progress.Kind == commandProgressFFmpeg && isFFmpegCommand(name) {
		runArgs = addFFmpegProgressArgs(runArgs)
	}
	tracker := newCommandProgressTracker(e.repo, taskUUID, stepName, progress)
	tracker.start()
	displaySummary := commandSummary(name, displayArgs)
	if strings.TrimSpace(summary) != "" {
		displaySummary = strings.TrimSpace(summary)
	}
	_ = e.repo.UpdateStepCommandSummary(context.Background(), taskUUID, stepName, displaySummary)
	result, err := e.opt.CommandRunner.Run(ctx, runner.Spec{
		Name: name, Args: runArgs, GracePeriod: 5 * time.Second, OnLine: tracker.onLine,
	})
	if err != nil {
		return commandTaskError(code, name, displayArgs, result, err, true)
	}
	tracker.finish()
	return nil
}

func isFFmpegCommand(name string) bool {
	return filepath.Base(name) == "ffmpeg"
}

func addFFmpegProgressArgs(args []string) []string {
	for _, arg := range args {
		if arg == "-progress" {
			return args
		}
	}
	insertAt := 0
	for i, arg := range args {
		if arg == "-y" {
			insertAt = i + 1
			break
		}
	}
	result := make([]string, 0, len(args)+3)
	result = append(result, args[:insertAt]...)
	result = append(result, "-progress", "pipe:1", "-nostats")
	result = append(result, args[insertAt:]...)
	return result
}

func commandSummary(name string, args []string) string {
	return strings.Join(commandParts(name, args), " ")
}

func commandParts(name string, args []string) []string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	parts = append(parts, args...)
	return parts
}

func commandErrorMessage(stderr string, err error) string {
	var last string
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			last = line
		}
	}
	if last != "" {
		return last
	}
	return err.Error()
}

func commandTaskError(code task.ErrorCode, name string, args []string, result runner.Result, err error, retryable bool) *task.Error {
	message := commandErrorMessage(result.Stderr, err)
	taskErr := task.NewError(code, message, retryable)
	taskErr.Details = commandFailureDetails(name, args, result, err)
	return taskErr
}

func commandFailureDetails(name string, args []string, result runner.Result, err error) map[string]any {
	details := map[string]any{
		"command":         commandParts(name, args),
		"command_summary": commandSummary(name, args),
		"exit_code":       result.ExitCode,
		"error":           err.Error(),
	}
	if !result.Started.IsZero() && !result.Finished.IsZero() {
		details["duration_seconds"] = seconds(result.Finished.Sub(result.Started))
	}
	if stdout := tailLines(result.Stdout, 20); stdout != "" {
		details["stdout_tail"] = stdout
	}
	if stderr := tailLines(result.Stderr, 20); stderr != "" {
		details["stderr_tail"] = stderr
	}
	return details
}

func tailLines(value string, maxLines int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxLines <= 0 {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}
