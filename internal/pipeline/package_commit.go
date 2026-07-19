package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"forge_worker/internal/state"
	"forge_worker/internal/storage"
	"forge_worker/internal/task"
)

func ensurePackagerDirs(root, workPrefix string, artifacts []packagedArtifactSpec) error {
	for _, artifact := range artifacts {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(workObjectKey(workPrefix, artifact.RelativePath)))), 0o700); err != nil {
			return fmt.Errorf("create packager output directory: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(workObjectKey(workPrefix, "master.m3u8")))), 0o700); err != nil {
		return fmt.Errorf("create packager output directory: %w", err)
	}
	return nil
}

func commitPackagedOutputs(ctx context.Context, root, workPrefix string, artifacts []packagedArtifactSpec) error {
	local, err := storage.NewLocal(root)
	if err != nil {
		return err
	}
	committedSegments := make(map[string]bool)
	for _, artifact := range artifacts {
		if _, err := local.Commit(ctx, workObjectKey(workPrefix, artifact.RelativePath), artifact.RelativePath); err != nil {
			return fmt.Errorf("commit packaged artifact %s: %w", artifact.RelativePath, err)
		}
		metadata, ok := artifact.Metadata.(packagedTrackMetadata)
		if !ok || metadata.SegmentPattern == "" || committedSegments[metadata.ID] {
			continue
		}
		committedSegments[metadata.ID] = true
		if err := commitTrackSegments(ctx, local, root, workPrefix, metadata); err != nil {
			return err
		}
	}
	if _, err := local.Commit(ctx, workObjectKey(workPrefix, "master.m3u8"), "master.m3u8"); err != nil {
		return fmt.Errorf("commit packaged artifact %s: %w", "master.m3u8", err)
	}
	return nil
}

func commitTrackSegments(ctx context.Context, local *storage.Local, root, workPrefix string, metadata packagedTrackMetadata) error {
	workDir := filepath.Dir(filepath.Join(root, filepath.FromSlash(workObjectKey(workPrefix, metadata.SegmentPattern))))
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return fmt.Errorf("read packaged segments for %s: %w", metadata.ID, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".m4s" {
			continue
		}
		finalKey := filepath.ToSlash(filepath.Join(filepath.Dir(metadata.SegmentPattern), entry.Name()))
		if _, err := local.Commit(ctx, workObjectKey(workPrefix, finalKey), finalKey); err != nil {
			return fmt.Errorf("commit packaged segment %s: %w", finalKey, err)
		}
	}
	return nil
}

func workObjectKey(prefix, relativePath string) string {
	return filepath.ToSlash(filepath.Join(prefix, filepath.FromSlash(relativePath)))
}

func (e *Executor) recordPackagedSegments(ctx context.Context, request task.Request, stepName string, artifacts []packagedArtifactSpec) error {
	seen := make(map[string]bool)
	for _, spec := range artifacts {
		metadata, ok := spec.Metadata.(packagedTrackMetadata)
		if !ok || metadata.SegmentPattern == "" || seen[metadata.ID] {
			continue
		}
		seen[metadata.ID] = true
		dir := filepath.Dir(filepath.Join(taskRoot(request), filepath.FromSlash(metadata.SegmentPattern)))
		entries, err := os.ReadDir(dir)
		if err != nil {
			return task.NewError(task.ErrPackagingFailed, fmt.Sprintf("read packaged segments for %s: %v", metadata.ID, err), true)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".m4s" {
				continue
			}
			relativePath := filepath.ToSlash(filepath.Join(filepath.Dir(metadata.SegmentPattern), entry.Name()))
			if err := e.recordArtifact(ctx, request, state.ArtifactSpec{StepName: stepName, Kind: metadata.Kind + "_segment", RelativePath: relativePath, Committed: true, Metadata: segmentMetadata{TrackID: metadata.ID, Kind: metadata.Kind}}); err != nil {
				return err
			}
		}
	}
	return nil
}
