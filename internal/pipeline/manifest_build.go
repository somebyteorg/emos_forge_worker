package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"forge_worker/internal/encryption"
	"forge_worker/internal/manifest"
	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func (e *Executor) finalize(ctx context.Context, request task.Request, record state.TaskRecord) error {
	probe, err := e.loadProbe(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	artifacts, err := e.repo.ListArtifacts(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	warnings, err := e.repo.ListWarnings(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	var keys encryption.File
	if hasAVRequest(request) && normalizedEncryptionMode(e.opt.EncryptionMode) == media.PackageEncryptionClearKey {
		keys, err = keysFromPackagedArtifacts(request.TaskUUID, artifacts)
		if err != nil {
			return task.NewError(task.ErrPackagingFailed, err.Error(), true)
		}
	}
	var hls hlsManifestData
	if hasAVRequest(request) {
		hls, err = loadHLSManifestData(taskRoot(request), artifacts)
		if err != nil {
			return task.NewError(task.ErrOutputValidationFailed, err.Error(), true)
		}
	}
	m := manifest.Manifest{
		SchemaVersion: 1, TaskUUID: request.TaskUUID, Status: "succeeded",
		CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339),
		Source:    sourceManifest(probe, request), Playback: playbackManifest(request, e.opt),
	}
	m.Playback.IndependentSegments = hls.Master.IndependentSegments
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "video_packaged":
			track, err := manifestVideoTrack(artifact, keys, hls)
			if err != nil {
				return task.NewError(task.ErrOutputValidationFailed, err.Error(), false)
			}
			track.Metadata, err = e.probeTrackMetadata(ctx, request, track.PlaylistPath)
			if err != nil {
				return err
			}
			m.VideoTracks = append(m.VideoTracks, track)
		case "audio_packaged":
			track, err := manifestAudioTrack(artifact, keys, hls)
			if err != nil {
				return task.NewError(task.ErrOutputValidationFailed, err.Error(), false)
			}
			track.Metadata, err = e.probeTrackMetadata(ctx, request, track.PlaylistPath)
			if err != nil {
				return err
			}
			m.AudioTracks = append(m.AudioTracks, track)
		case "subtitle":
			m.Subtitles = append(m.Subtitles, manifestSubtitle(artifact))
		}
	}
	m.Sprites, err = manifestSprites(artifacts)
	if err != nil {
		return task.NewError(task.ErrOutputValidationFailed, err.Error(), false)
	}
	sortManifest(&m)
	now := time.Now().UTC()
	m.CompletedAt = now.Format(time.RFC3339)
	steps, err := e.repo.ListSteps(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	steps = finalizedProcessingSteps(steps, now)
	commands, err := e.repo.ListStepCommands(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	log := processingManifest(record, now, steps, commands, artifacts, e.opt)
	log["warnings"] = manifestWarnings(warnings)
	if err := manifest.Write(filepath.Join(taskRoot(request), "manifest.json"), m); err != nil {
		return task.NewError(task.ErrOutputValidationFailed, err.Error(), true)
	}
	if err := manifest.WriteLog(filepath.Join(taskRoot(request), "log.json"), log); err != nil {
		return task.NewError(task.ErrOutputValidationFailed, err.Error(), true)
	}
	if err := e.recordArtifact(ctx, request, state.ArtifactSpec{StepName: StepFinalize, Kind: "manifest", RelativePath: "manifest.json", Committed: true, Metadata: map[string]any{"schema_version": 1}}); err != nil {
		return err
	}
	if err := e.recordArtifact(ctx, request, state.ArtifactSpec{StepName: StepFinalize, Kind: "log", RelativePath: "log.json", Committed: true, Metadata: map[string]any{"schema_version": 1}}); err != nil {
		return err
	}
	if !e.opt.EnableRecovery {
		if err := cleanupTaskTmpDir(request); err != nil {
			return task.NewError(task.ErrOutputValidationFailed, err.Error(), true)
		}
	}
	return nil
}

func finalizedProcessingSteps(steps []state.StepRecord, finishedAt time.Time) []state.StepRecord {
	result := append([]state.StepRecord(nil), steps...)
	for i := range result {
		if result[i].Name != StepFinalize {
			continue
		}
		if result[i].State == string(task.StepSucceeded) {
			return result
		}
		result[i].State = string(task.StepSucceeded)
		result[i].Progress = 100
		result[i].FinishedAt = &finishedAt
		return result
	}
	return result
}

func stepTimesFromFinalizedSteps(steps []state.StepRecord) []StepTime {
	result := make([]StepTime, 0, len(steps))
	for _, step := range steps {
		if step.StartedAt == nil || step.FinishedAt == nil {
			continue
		}
		result = append(result, StepTime{
			Name:     ExternalStepName(step.Name),
			Duration: seconds(step.FinishedAt.Sub(*step.StartedAt)),
		})
	}
	return result
}

func (e *Executor) probeTrackMetadata(ctx context.Context, request task.Request, playlistPath string) (map[string]any, error) {
	path := filepath.Join(taskRoot(request), filepath.FromSlash(playlistPath))
	args, err := media.BuildMetadataProbeArgs(path)
	if err != nil {
		return nil, task.NewError(task.ErrOutputValidationFailed, err.Error(), false)
	}
	_ = e.repo.UpdateStepCommandSummary(context.Background(), request.TaskUUID, StepFinalize, metadataProbeCommandSummary(e.opt.FFprobePath, args, playlistPath))
	metadata, err := media.RunMetadataProbeWithRunner(ctx, e.opt.ProbeRunner, e.opt.FFprobePath, path)
	if err != nil {
		return nil, task.NewError(task.ErrOutputValidationFailed, err.Error(), true)
	}
	normalizeMetadataFilename(metadata, playlistPath)
	return metadata, nil
}

func metadataProbeCommandSummary(name string, args []string, playlistPath string) string {
	return fmt.Sprintf("metadata probe | %s | %s", filepath.ToSlash(playlistPath), commandSummary(name, args))
}

func normalizeMetadataFilename(metadata map[string]any, relativePath string) {
	format, ok := metadata["format"].(map[string]any)
	if !ok {
		return
	}
	format["filename"] = filepath.ToSlash(relativePath)
}

func cleanupTaskTmpDir(request task.Request) error {
	return CleanupTaskTemporaryFiles(taskRoot(request))
}

func CleanupTaskTemporaryFiles(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("task root must be an absolute path")
	}
	tmpDir := filepath.Join(root, "tmp")
	if err := os.RemoveAll(tmpDir); err != nil {
		return fmt.Errorf("remove task tmp directory: %w", err)
	}
	return nil
}
