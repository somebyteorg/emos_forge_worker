package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forge_worker/internal/download"
	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func (e *Executor) prepare(ctx context.Context, request task.Request) error {
	if err := os.MkdirAll(filepath.Join(taskRoot(request), "tmp"), 0o700); err != nil {
		return task.NewError(task.ErrInputNotReadable, fmt.Sprintf("create task tmp directory: %v", err), true)
	}
	if request.Input.Type != task.InputLocal {
		return nil
	}
	path, err := filepath.EvalSymlinks(request.Input.URI)
	if err != nil {
		if os.IsNotExist(err) {
			return task.NewError(task.ErrInputNotFound, "local input does not exist", false)
		}
		return task.NewError(task.ErrInputNotReadable, err.Error(), false)
	}
	info, err := os.Stat(path)
	if err != nil {
		return task.NewError(task.ErrInputNotReadable, err.Error(), false)
	}
	if !info.Mode().IsRegular() {
		return task.NewError(task.ErrInputNotReadable, "local input must be a regular file", false)
	}
	file, err := os.Open(path)
	if err != nil {
		return task.NewError(task.ErrInputNotReadable, err.Error(), false)
	}
	if err := file.Close(); err != nil {
		return task.NewError(task.ErrInputNotReadable, err.Error(), false)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (e *Executor) downloadURL(ctx context.Context, request task.Request) error {
	_, err := e.opt.Downloader.Download(ctx, download.Request{
		URI:         request.Input.URI,
		PartialPath: downloadedInputPath(request) + ".partial",
		FinalPath:   downloadedInputPath(request),
	})
	if err != nil {
		return task.NewError(task.ErrDownloadFailed, err.Error(), true)
	}
	return e.recordArtifact(ctx, request, state.ArtifactSpec{
		StepName: StepDownloadURL, Kind: "downloaded_input", RelativePath: filepath.ToSlash(filepath.Join("tmp", "input.mkv")), Committed: true,
	})
}

func (e *Executor) probe(ctx context.Context, request task.Request) error {
	probe, err := media.RunProbeWithRunner(ctx, e.opt.ProbeRunner, e.opt.FFprobePath, preparedInputPath(request))
	if err != nil {
		return err
	}
	return e.repo.SetTaskProbe(ctx, request.TaskUUID, probe)
}

func (e *Executor) validateInput(ctx context.Context, request task.Request) error {
	probe, err := e.loadProbe(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	if details, ok := dolbyVisionProbeDetails(probe); ok {
		return taskErrorWithDetails(task.ErrUnsupportedDolbyVision, "Dolby Vision video is not supported by this worker", false, details)
	}
	if (request.Steps.Video.Enabled || request.Steps.Sprites.Enabled) && len(probe.VideoStreams) == 0 {
		return task.NewError(task.ErrUnsupportedMedia, "input has no video stream", false)
	}
	if request.Steps.Audio.Enabled && len(probe.AudioStreams) == 0 {
		return task.NewError(task.ErrNoPlayableAudio, "input has no audio stream", false)
	}
	if request.Steps.Subtitles.Required && len(probe.Subtitles) == 0 {
		return task.NewError(task.ErrUnsupportedMedia, "required subtitles are missing", false)
	}
	if _, err := os.Stat(preparedInputPath(request)); err != nil {
		return task.NewError(task.ErrInputNotReadable, err.Error(), false)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (e *Executor) loadProbe(ctx context.Context, taskUUID string) (media.Probe, error) {
	record, err := e.repo.GetTask(ctx, taskUUID)
	if err != nil {
		return media.Probe{}, err
	}
	if strings.TrimSpace(record.ProbeJSON) == "" {
		return media.Probe{}, task.NewError(task.ErrProbeFailed, "probe result is missing", true)
	}
	var probe media.Probe
	if err := json.Unmarshal([]byte(record.ProbeJSON), &probe); err != nil {
		return media.Probe{}, task.NewError(task.ErrProbeFailed, "stored probe result could not be parsed", true)
	}
	return probe, nil
}
