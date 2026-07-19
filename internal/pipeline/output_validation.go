package pipeline

import (
	"context"
	"fmt"

	"forge_worker/internal/storage"
	"forge_worker/internal/task"
)

func (e *Executor) validateOutput(ctx context.Context, request task.Request) error {
	artifacts, err := e.repo.ListArtifacts(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	local, err := storage.NewLocal(taskRoot(request))
	if err != nil {
		return task.NewError(task.ErrOutputValidationFailed, err.Error(), true)
	}
	for _, artifact := range artifacts {
		if !artifact.Committed {
			continue
		}
		actual, err := local.Stat(ctx, artifact.RelativePath)
		if err != nil {
			return taskErrorWithDetails(task.ErrOutputValidationFailed, fmt.Sprintf("artifact %s is missing or unreadable: %v", artifact.RelativePath, err), true, map[string]any{
				"artifact": artifact.RelativePath,
				"kind":     artifact.Kind,
				"root":     taskRoot(request),
			})
		}
		if actual.SizeBytes != artifact.SizeBytes {
			return taskErrorWithDetails(task.ErrOutputValidationFailed, fmt.Sprintf("artifact %s size changed", artifact.RelativePath), true, map[string]any{
				"artifact":            artifact.RelativePath,
				"kind":                artifact.Kind,
				"expected_size_bytes": artifact.SizeBytes,
				"actual_size_bytes":   actual.SizeBytes,
			})
		}
	}
	if hasAVRequest(request) {
		required := []string{"master.m3u8"}
		for _, path := range required {
			if ok, err := local.Exists(ctx, path); err != nil || !ok {
				return taskErrorWithDetails(task.ErrOutputValidationFailed, fmt.Sprintf("required output %s is missing", path), true, map[string]any{
					"required_output": path,
					"root":            taskRoot(request),
				})
			}
		}
		for _, artifact := range artifacts {
			if artifact.Kind != "video_packaged" && artifact.Kind != "audio_packaged" {
				continue
			}
			metadata, err := packagedTrackMetadataFromArtifact(artifact)
			if err != nil {
				return task.NewError(task.ErrOutputValidationFailed, err.Error(), false)
			}
			if segmentCount(metadata.ID, artifacts) == 0 {
				return taskErrorWithDetails(task.ErrOutputValidationFailed, fmt.Sprintf("packaged track %s has no recorded segments", metadata.ID), true, map[string]any{
					"track_id":        metadata.ID,
					"kind":            metadata.Kind,
					"playlist_path":   metadata.PlaylistPath,
					"segment_pattern": metadata.SegmentPattern,
				})
			}
		}
	}
	return nil
}
