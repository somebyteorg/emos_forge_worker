package pipeline

import (
	"context"
	"path/filepath"
	"strings"

	"forge_worker/internal/state"
	"forge_worker/internal/storage"
	"forge_worker/internal/task"
)

func (e *Executor) recordArtifact(ctx context.Context, request task.Request, spec state.ArtifactSpec) error {
	local, err := storage.NewLocal(taskRoot(request))
	if err != nil {
		return err
	}
	artifact, err := local.Stat(ctx, spec.RelativePath)
	if err != nil {
		return err
	}
	spec.SizeBytes = artifact.SizeBytes
	spec.RelativePath = artifact.ObjectKey
	return e.repo.UpsertArtifact(ctx, request.TaskUUID, spec)
}

func (e *Executor) recordArtifactSpecs(ctx context.Context, request task.Request, specs []state.ArtifactSpec) error {
	for _, spec := range specs {
		if err := e.recordArtifact(ctx, request, spec); err != nil {
			return err
		}
	}
	return nil
}

func taskRoot(request task.Request) string {
	return filepath.Join(request.Output.Root, request.TaskUUID)
}

func downloadedInputPath(request task.Request) string {
	return filepath.Join(taskRoot(request), "tmp", "input.mkv")
}

func preparedInputPath(request task.Request) string {
	if request.Input.Type == task.InputURL {
		return downloadedInputPath(request)
	}
	return request.Input.URI
}

func safeFileSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
