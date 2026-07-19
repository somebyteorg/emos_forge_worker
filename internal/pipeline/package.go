package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"forge_worker/internal/encryption"
	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func (e *Executor) packageTracks(ctx context.Context, request task.Request, step state.StepRecord) error {
	artifacts, err := e.repo.ListArtifacts(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	plan, err := buildPackagePlan(artifacts, videoProfilesInclude(request.Steps.Video.Profiles, "package"), audioPackageRequested(request))
	if err != nil {
		return err
	}
	if len(plan.Tracks) == 0 {
		return task.NewError(task.ErrPackagingFailed, "no audio or video intermediates are available for packaging", false)
	}
	e.setStepProgress(request.TaskUUID, step.Name, 5)
	root := taskRoot(request)
	workPrefix := filepath.ToSlash(filepath.Join("tmp", "work", "package"))
	if err := e.cleanupPackageAttempt(ctx, request, step.Name, workPrefix, plan.Artifacts); err != nil {
		return task.NewError(task.ErrPackagingFailed, err.Error(), true)
	}
	encryptionMode := normalizedEncryptionMode(e.opt.EncryptionMode)
	var keys encryption.File
	if encryptionMode == media.PackageEncryptionClearKey {
		keyTracks := make([]encryption.TrackSpec, 0, len(plan.Tracks))
		for _, track := range plan.Tracks {
			keyTracks = append(keyTracks, encryption.TrackSpec{TrackID: track.TrackID, Kind: track.Kind, VideoID: track.VideoID})
		}
		keys, err = encryption.Generate(request.TaskUUID, keyTracks, time.Now().UTC())
		if err != nil {
			return task.NewError(task.ErrPackagingFailed, err.Error(), true)
		}
		attachPackageKeysToArtifacts(&plan, keys)
	}
	for i := range plan.Tracks {
		plan.Tracks[i].Input = filepath.Join(root, filepath.FromSlash(plan.Tracks[i].Input))
		plan.Tracks[i].InitSegment = filepath.Join(root, filepath.FromSlash(workObjectKey(workPrefix, plan.Tracks[i].InitSegment)))
		plan.Tracks[i].SegmentTemplate = filepath.Join(root, filepath.FromSlash(workObjectKey(workPrefix, plan.Tracks[i].SegmentTemplate)))
		plan.Tracks[i].PlaylistName = filepath.Join(root, filepath.FromSlash(workObjectKey(workPrefix, plan.Tracks[i].PlaylistName)))
	}
	segmentDuration := plan.SegmentDuration
	if segmentDuration <= 0 {
		segmentDuration = e.opt.SegmentTarget
	}
	args, err := media.BuildPackagerArgs(media.PackageSpec{
		Tracks: plan.Tracks, Keys: keys, EncryptionMode: encryptionMode, SegmentDuration: segmentDuration, DefaultLanguage: plan.DefaultLanguage,
		HLSMaster: filepath.Join(root, filepath.FromSlash(workObjectKey(workPrefix, "master.m3u8"))),
	})
	if err != nil {
		return task.NewError(task.ErrPackagingFailed, err.Error(), false)
	}
	if err := ensurePackagerDirs(root, workPrefix, plan.Artifacts); err != nil {
		return task.NewError(task.ErrPackagingFailed, err.Error(), true)
	}
	e.setStepProgress(request.TaskUUID, step.Name, 8)
	summary := packageCommandSummary(e.opt.PackagerPath, args, plan.Tracks)
	if err := e.runCommandWithProgressSummary(ctx, request.TaskUUID, step.Name, e.opt.PackagerPath, args, summary, task.ErrPackagingFailed, stageCommandProgress(8, 70)); err != nil {
		return err
	}
	e.setStepProgress(request.TaskUUID, step.Name, 75)
	if plan.DefaultAudioPlaylist != "" {
		masterPath := filepath.Join(root, filepath.FromSlash(workObjectKey(workPrefix, "master.m3u8")))
		if err := setHLSDefaultAudio(masterPath, plan.DefaultAudioPlaylist); err != nil {
			return task.NewError(task.ErrPackagingFailed, err.Error(), true)
		}
	}
	e.setStepProgress(request.TaskUUID, step.Name, 80)
	e.setStepProgress(request.TaskUUID, step.Name, 84)
	if err := commitPackagedOutputs(ctx, root, workPrefix, plan.Artifacts); err != nil {
		return task.NewError(task.ErrPackagingFailed, err.Error(), true)
	}
	if err := appendForgeUUIDTags(request, packagedInitPaths(root, plan.Artifacts), task.ErrPackagingFailed); err != nil {
		return err
	}
	e.setStepProgress(request.TaskUUID, step.Name, 90)
	outputs := append([]packagedArtifactSpec(nil), plan.Artifacts...)
	outputs = append(outputs, packagedArtifactSpec{Kind: "hls_master", RelativePath: "master.m3u8"})
	for _, spec := range outputs {
		if err := e.recordArtifact(ctx, request, state.ArtifactSpec{StepName: step.Name, Kind: spec.Kind, RelativePath: spec.RelativePath, Committed: true, Metadata: spec.Metadata}); err != nil {
			return err
		}
	}
	e.setStepProgress(request.TaskUUID, step.Name, 94)
	if err := e.recordPackagedSegments(ctx, request, step.Name, plan.Artifacts); err != nil {
		return err
	}
	e.setStepProgress(request.TaskUUID, step.Name, 98)
	return nil
}

func (e *Executor) cleanupPackageAttempt(ctx context.Context, request task.Request, stepName, workPrefix string, artifacts []packagedArtifactSpec) error {
	if err := e.deleteStepArtifacts(ctx, request, stepName); err != nil {
		return err
	}
	if err := removeTaskRelativeDir(request, workPrefix); err != nil {
		return err
	}
	directories := make(map[string]bool)
	for _, artifact := range artifacts {
		directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(artifact.RelativePath)))
		if directory == "." || directories[directory] {
			continue
		}
		directories[directory] = true
		if err := removeTaskRelativeDir(request, directory); err != nil {
			return err
		}
	}
	return removeTaskRelativeFile(request, "master.m3u8")
}

func packageCommandSummary(name string, args []string, tracks []media.PackageTrack) string {
	audioIDs := make([]string, 0, len(tracks))
	videoIDs := make([]string, 0, len(tracks))
	for _, track := range tracks {
		switch track.Kind {
		case "audio":
			audioIDs = append(audioIDs, track.TrackID)
		case "video":
			videoIDs = append(videoIDs, track.TrackID)
		}
	}
	parts := []string{fmt.Sprintf("package %d tracks", len(tracks))}
	if len(videoIDs) > 0 {
		parts = append(parts, "video "+strings.Join(videoIDs, ","))
	}
	if len(audioIDs) > 0 {
		parts = append(parts, "audio "+strings.Join(audioIDs, ","))
	}
	return strings.Join(parts, " | ") + " | " + commandSummary(name, args)
}

func packagedInitPaths(root string, artifacts []packagedArtifactSpec) []string {
	paths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "video_packaged", "audio_packaged":
			paths = append(paths, filepath.Join(root, filepath.FromSlash(artifact.RelativePath)))
		}
	}
	return paths
}

func attachPackageKeysToArtifacts(plan *packagePlan, keys encryption.File) {
	if len(keys.Tracks) == 0 {
		return
	}
	for i := range plan.Artifacts {
		metadata, ok := plan.Artifacts[i].Metadata.(packagedTrackMetadata)
		if !ok {
			continue
		}
		key, ok := keyForTrack(keys, metadata.ID)
		if !ok {
			continue
		}
		metadata.KeyIDHex = key.KeyIDHex
		metadata.KeyHex = key.KeyHex
		plan.Artifacts[i].Metadata = metadata
	}
}
