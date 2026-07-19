package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"forge_worker/internal/state"
	"forge_worker/internal/storage"
	"forge_worker/internal/task"
)

const pipelineCheckpointVersion = 1
const pipelineCheckpointName = "pipeline_state.json"

type pipelineCheckpoint struct {
	SchemaVersion int                  `json:"schema_version"`
	Settings      checkpointSettings   `json:"settings"`
	Source        sourceFingerprint    `json:"source"`
	State         state.TaskCheckpoint `json:"state"`
}

type checkpointSettings struct {
	AudioChannels       int    `json:"audio_channels"`
	EncryptionMode      string `json:"encryption_mode"`
	SegmentTargetMillis int64  `json:"segment_target_millis"`
	SegmentMaxMillis    int64  `json:"segment_max_millis"`
}

type sourceFingerprint struct {
	SizeBytes        int64 `json:"size_bytes,omitempty"`
	ModifiedUnixNano int64 `json:"modified_unix_nano,omitempty"`
}

func (e *Executor) saveCheckpoint(ctx context.Context, request task.Request) error {
	snapshot, err := e.repo.SnapshotTask(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	source, err := e.currentCheckpointSource(request)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(pipelineCheckpoint{
		SchemaVersion: pipelineCheckpointVersion,
		Settings:      e.checkpointSettings(),
		Source:        source,
		State:         snapshot,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pipeline checkpoint: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Join(taskRoot(request), "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create pipeline checkpoint directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".pipeline_state-*.json")
	if err != nil {
		return fmt.Errorf("create pipeline checkpoint: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure pipeline checkpoint: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write pipeline checkpoint: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync pipeline checkpoint: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close pipeline checkpoint: %w", err)
	}
	path := filepath.Join(dir, pipelineCheckpointName)
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit pipeline checkpoint: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open pipeline checkpoint directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync pipeline checkpoint directory: %w", err)
	}
	return nil
}

func (e *Executor) restoreCheckpoint(ctx context.Context, request task.Request) (RecoveryInfo, error) {
	path := filepath.Join(taskRoot(request), "tmp", pipelineCheckpointName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return RecoveryInfo{}, nil
	}
	if err != nil {
		return RecoveryInfo{}, fmt.Errorf("read pipeline checkpoint: %w", err)
	}
	var checkpoint pipelineCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return RecoveryInfo{}, fmt.Errorf("decode pipeline checkpoint: %w", err)
	}
	if checkpoint.SchemaVersion != pipelineCheckpointVersion {
		return RecoveryInfo{}, fmt.Errorf("unsupported pipeline checkpoint schema version %d", checkpoint.SchemaVersion)
	}
	currentRequest, err := json.Marshal(request)
	if err != nil {
		return RecoveryInfo{}, fmt.Errorf("encode current pipeline request: %w", err)
	}
	savedRequest, err := json.Marshal(checkpoint.State.Request)
	if err != nil {
		return RecoveryInfo{}, fmt.Errorf("encode saved pipeline request: %w", err)
	}
	if !bytes.Equal(currentRequest, savedRequest) {
		return RecoveryInfo{}, task.NewError(task.ErrTaskOutputConflict, "pipeline checkpoint belongs to a different request", false)
	}
	currentSource, err := e.currentCheckpointSource(request)
	if err != nil {
		return RecoveryInfo{}, err
	}

	plannedSteps, err := e.repo.ListSteps(ctx, request.TaskUUID)
	if err != nil {
		return RecoveryInfo{}, err
	}
	savedSteps := make(map[string]state.StepRecord, len(checkpoint.State.Steps))
	for _, step := range checkpoint.State.Steps {
		if _, exists := savedSteps[step.Name]; exists {
			return RecoveryInfo{}, fmt.Errorf("pipeline checkpoint contains duplicate step %s", step.Name)
		}
		savedSteps[step.Name] = step
	}
	artifactsByStep := make(map[string][]state.ArtifactRecord)
	for _, artifact := range checkpoint.State.Artifacts {
		artifactsByStep[artifact.StepName] = append(artifactsByStep[artifact.StepName], artifact)
	}
	local, err := storage.NewLocal(taskRoot(request))
	if err != nil {
		return RecoveryInfo{}, err
	}
	info := RecoveryInfo{CheckpointFound: true}
	recovered := make(map[string]bool, len(plannedSteps))
	recoveryCompatible := true
	if checkpoint.Settings != e.checkpointSettings() {
		info.InvalidStep = StepPrepare
		info.InvalidReason = "pipeline settings changed"
		recoveryCompatible = false
	}
	if checkpoint.Source != currentSource {
		info.InvalidStep = StepPrepare
		info.InvalidReason = "input file changed"
		recoveryCompatible = false
	}
	for _, planned := range plannedSteps {
		if !recoveryCompatible {
			break
		}
		saved, ok := savedSteps[planned.Name]
		if !ok || saved.Kind != planned.Kind {
			break
		}
		stepState := task.StepState(saved.State)
		if stepState != task.StepSucceeded && stepState != task.StepSkipped {
			break
		}
		valid := true
		for _, artifact := range artifactsByStep[planned.Name] {
			actual, statErr := local.Stat(ctx, artifact.RelativePath)
			if statErr != nil || actual.SizeBytes != artifact.SizeBytes {
				info.InvalidStep = planned.Name
				info.InvalidArtifact = artifact.RelativePath
				if removeErr := removeTaskRelativeFile(request, artifact.RelativePath); removeErr != nil {
					return RecoveryInfo{}, fmt.Errorf("remove invalid checkpoint artifact %s: %w", artifact.RelativePath, removeErr)
				}
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		recovered[planned.Name] = true
		info.RecoveredSteps = append(info.RecoveredSteps, planned.Name)
	}

	restored := checkpoint.State
	restored.Steps = restored.Steps[:0]
	for _, planned := range plannedSteps {
		if !recovered[planned.Name] {
			break
		}
		restored.Steps = append(restored.Steps, savedSteps[planned.Name])
	}
	restored.Artifacts = filterRecoveredArtifacts(checkpoint.State.Artifacts, recovered)
	restored.Warnings = filterRecoveredWarnings(checkpoint.State.Warnings, recovered)
	restored.Commands = filterRecoveredCommands(checkpoint.State.Commands, recovered)
	restored.Task.TaskUUID = request.TaskUUID
	restored.Task.Progress = 0
	if !recovered[StepProbe] {
		restored.Task.ProbeJSON = ""
	}
	if len(restored.Steps) == len(plannedSteps) && recovered[StepFinalize] {
		restored.Task.State = task.StateSucceeded
	} else if len(restored.Steps) == 0 {
		restored.Task.State = task.StateDiscovered
	} else {
		restored.Task.State = taskStateForStep(plannedSteps[len(restored.Steps)].Name)
	}
	if err := e.repo.RestoreTask(ctx, restored); err != nil {
		return RecoveryInfo{}, err
	}
	return info, nil
}

func (e *Executor) checkpointSettings() checkpointSettings {
	return checkpointSettings{
		AudioChannels:       e.opt.AudioChannels,
		EncryptionMode:      normalizedEncryptionMode(e.opt.EncryptionMode),
		SegmentTargetMillis: e.opt.SegmentTarget.Milliseconds(),
		SegmentMaxMillis:    e.opt.SegmentMax.Milliseconds(),
	}
}

func checkpointSourceFingerprint(request task.Request) (sourceFingerprint, error) {
	if request.Input.Type != task.InputLocal {
		return sourceFingerprint{}, nil
	}
	info, err := os.Stat(request.Input.URI)
	if err != nil {
		return sourceFingerprint{}, fmt.Errorf("stat checkpoint input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return sourceFingerprint{}, fmt.Errorf("checkpoint input is not a regular file")
	}
	return sourceFingerprint{SizeBytes: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano()}, nil
}

func (e *Executor) currentCheckpointSource(request task.Request) (sourceFingerprint, error) {
	if e.checkpointSourceSet {
		return e.checkpointSource, nil
	}
	source, err := checkpointSourceFingerprint(request)
	if err != nil {
		return sourceFingerprint{}, err
	}
	e.checkpointSource = source
	e.checkpointSourceSet = true
	return source, nil
}

func filterRecoveredArtifacts(records []state.ArtifactRecord, recovered map[string]bool) []state.ArtifactRecord {
	result := make([]state.ArtifactRecord, 0, len(records))
	for _, record := range records {
		if recovered[record.StepName] {
			result = append(result, record)
		}
	}
	return result
}

func filterRecoveredWarnings(records []state.WarningRecord, recovered map[string]bool) []state.WarningRecord {
	result := make([]state.WarningRecord, 0, len(records))
	for _, record := range records {
		if recovered[record.StepName] {
			result = append(result, record)
		}
	}
	return result
}

func filterRecoveredCommands(records []state.StepCommandRecord, recovered map[string]bool) []state.StepCommandRecord {
	result := make([]state.StepCommandRecord, 0, len(records))
	for _, record := range records {
		if recovered[record.StepName] {
			result = append(result, record)
		}
	}
	return result
}
