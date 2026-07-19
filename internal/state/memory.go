package state

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"forge_worker/internal/task"
)

type DB struct {
	mu    sync.RWMutex
	tasks map[string]*taskData
}

type taskData struct {
	record      TaskRecord
	requestJSON string
	steps       []*stepEntry
	stepsByName map[string]*stepEntry
	artifacts   map[string]ArtifactRecord
	warnings    []WarningRecord
	commands    []StepCommandRecord
}

type stepEntry struct {
	record       StepRecord
	weight       float64
	retryAt      *time.Time
	dependencies []string
}

func New() *DB {
	return &DB{tasks: make(map[string]*taskData)}
}

func (d *DB) EnsureTask(ctx context.Context, request task.Request, initial task.State) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return false, fmt.Errorf("encode task request: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing := d.tasks[request.TaskUUID]; existing != nil {
		if existing.requestJSON != string(payload) {
			return false, task.NewError(task.ErrTaskOutputConflict, "task UUID already exists with a different request", false)
		}
		return false, nil
	}
	d.tasks[request.TaskUUID] = &taskData{
		record: TaskRecord{
			TaskUUID:  request.TaskUUID,
			State:     initial,
			CreatedAt: time.Now().UTC(),
		},
		requestJSON: string(payload),
		stepsByName: make(map[string]*stepEntry),
		artifacts:   make(map[string]ArtifactRecord),
	}
	return true, nil
}

func (d *DB) GetTask(ctx context.Context, taskUUID string) (TaskRecord, error) {
	if err := ctx.Err(); err != nil {
		return TaskRecord{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return TaskRecord{}, err
	}
	return data.record, nil
}

func (d *DB) TransitionTaskTo(ctx context.Context, taskUUID string, to task.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return err
	}
	if data.record.State == to {
		return nil
	}
	if err := task.ValidateTransition(data.record.State, to); err != nil {
		return err
	}
	data.record.State = to
	return nil
}

func (d *DB) FinishTask(ctx context.Context, taskUUID string, final task.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !final.Terminal() {
		return fmt.Errorf("final task state must be terminal")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return err
	}
	if data.record.State == final {
		return nil
	}
	if err := task.ValidateTransition(data.record.State, final); err != nil {
		return err
	}
	data.record.State = final
	if final == task.StateSucceeded {
		data.record.Progress = 100
	}
	return nil
}

func (d *DB) SetTaskProbe(ctx context.Context, taskUUID string, probe any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(probe)
	if err != nil {
		return fmt.Errorf("encode task probe: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return err
	}
	data.record.ProbeJSON = string(payload)
	return nil
}

func (d *DB) EnsureSteps(ctx context.Context, taskUUID string, specs []StepSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return err
	}
	available := make(map[string]bool, len(data.stepsByName)+len(specs))
	for name := range data.stepsByName {
		available[name] = true
	}
	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if spec.Name == "" || spec.Kind == "" || spec.Weight <= 0 || spec.MaxAttempts <= 0 {
			return fmt.Errorf("invalid step spec for task %s", taskUUID)
		}
		if seen[spec.Name] {
			return fmt.Errorf("duplicate step spec %s", spec.Name)
		}
		seen[spec.Name] = true
		available[spec.Name] = true
	}
	for _, spec := range specs {
		for _, dependency := range spec.Dependencies {
			if dependency == "" {
				return fmt.Errorf("step %s has an empty dependency", spec.Name)
			}
			if !available[dependency] {
				return fmt.Errorf("step %s dependency %s does not exist", spec.Name, dependency)
			}
		}
	}
	for _, spec := range specs {
		if data.stepsByName[spec.Name] != nil {
			continue
		}
		entry := &stepEntry{record: StepRecord{
			Name: spec.Name, Kind: spec.Kind, State: string(task.StepPending),
			MaxAttempts: spec.MaxAttempts,
		}, weight: spec.Weight}
		data.steps = append(data.steps, entry)
		data.stepsByName[spec.Name] = entry
	}
	for _, spec := range specs {
		data.stepsByName[spec.Name].dependencies = append([]string(nil), spec.Dependencies...)
	}
	return nil
}

func (d *DB) ListSteps(ctx context.Context, taskUUID string) ([]StepRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return nil, err
	}
	result := make([]StepRecord, 0, len(data.steps))
	for _, step := range data.steps {
		result = append(result, cloneStepRecord(step.record))
	}
	return result, nil
}

func (d *DB) ListReadySteps(ctx context.Context, taskUUID string) ([]StepRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var result []StepRecord
	for _, step := range data.steps {
		if !stepMayRun(step, now) || !dependenciesSatisfied(data, step.dependencies) {
			continue
		}
		result = append(result, cloneStepRecord(step.record))
	}
	return result, nil
}

func (d *DB) StartStep(ctx context.Context, taskUUID, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, step, err := d.stepLocked(taskUUID, name)
	if err != nil {
		return err
	}
	if step.record.State != string(task.StepPending) && step.record.State != string(task.StepRetryWait) {
		return fmt.Errorf("step %s cannot be started from its current state", name)
	}
	now := time.Now().UTC()
	step.record.State = string(task.StepRunning)
	step.record.Progress = 0
	step.record.FPS = 0
	step.record.Speed = 0
	step.record.DetailsJSON = ""
	step.record.Attempt++
	if step.record.StartedAt == nil {
		step.record.StartedAt = &now
	}
	step.retryAt = nil
	recalculateTaskProgress(data)
	return nil
}

func (d *DB) UpdateStepProgress(ctx context.Context, taskUUID, name string, progress float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, step, err := d.stepLocked(taskUUID, name)
	if err != nil {
		return err
	}
	if step.record.State != string(task.StepRunning) {
		return nil
	}
	progress = clampPercent(progress)
	if step.record.Progress < progress {
		step.record.Progress = progress
		recalculateTaskProgress(data)
	}
	return nil
}

func (d *DB) UpdateStepPerformance(ctx context.Context, taskUUID, name string, fps, speed float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if math.IsNaN(fps) || math.IsInf(fps, 0) || fps < 0 {
		fps = 0
	}
	if math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0 {
		speed = 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, step, err := d.stepLocked(taskUUID, name)
	if err != nil {
		return err
	}
	if step.record.State != string(task.StepRunning) {
		return nil
	}
	step.record.FPS = fps
	step.record.Speed = speed
	return nil
}

func (d *DB) UpdateStepCommandSummary(ctx context.Context, taskUUID, name, summary string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, step, err := d.stepLocked(taskUUID, name)
	if err != nil {
		return err
	}
	if step.record.State != string(task.StepRunning) {
		return nil
	}
	step.record.CommandSummary = summary
	if summary != "" {
		data.commands = append(data.commands, StepCommandRecord{StepName: name, Summary: summary})
	}
	return nil
}

func (d *DB) CompleteStep(ctx context.Context, taskUUID, name string) error {
	return d.finishStep(ctx, taskUUID, name, task.StepSucceeded, nil)
}

func (d *DB) SkipStep(ctx context.Context, taskUUID, name string, details any) error {
	return d.finishStep(ctx, taskUUID, name, task.StepSkipped, details)
}

func (d *DB) FailStep(ctx context.Context, taskUUID, name string, stepErr any) error {
	return d.finishStep(ctx, taskUUID, name, task.StepFailed, stepErr)
}

func (d *DB) RetryStep(ctx context.Context, taskUUID, name string, retryAt time.Time, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	errorJSON, err := encodeOptionalJSON(payload)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, step, err := d.stepLocked(taskUUID, name)
	if err != nil {
		return err
	}
	if step.record.State != string(task.StepRunning) {
		return fmt.Errorf("step %s cannot be retried from its current state", name)
	}
	retry := retryAt.UTC()
	step.record.State = string(task.StepRetryWait)
	step.record.Progress = 0
	step.record.FPS = 0
	step.record.Speed = 0
	step.record.DetailsJSON = errorJSON
	step.retryAt = &retry
	recalculateTaskProgress(data)
	return nil
}

func (d *DB) SkipUnfinishedSteps(ctx context.Context, taskUUID string, details any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	errorJSON, err := encodeOptionalJSON(details)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, step := range data.steps {
		if task.StepState(step.record.State).Terminal() {
			continue
		}
		step.record.State = string(task.StepSkipped)
		step.record.Progress = 100
		step.record.FinishedAt = &now
		step.record.DetailsJSON = errorJSON
	}
	recalculateTaskProgress(data)
	return nil
}

func (d *DB) finishStep(ctx context.Context, taskUUID, name string, state task.StepState, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	errorJSON, err := encodeOptionalJSON(payload)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, step, err := d.stepLocked(taskUUID, name)
	if err != nil {
		return err
	}
	if task.StepState(step.record.State).Terminal() {
		return fmt.Errorf("step %s cannot be finished from its current state", name)
	}
	now := time.Now().UTC()
	step.record.State = string(state)
	step.record.Progress = 100
	if state == task.StepFailed {
		step.record.Progress = 0
	}
	step.record.FinishedAt = &now
	step.record.DetailsJSON = errorJSON
	recalculateTaskProgress(data)
	return nil
}

func (d *DB) UpsertArtifact(ctx context.Context, taskUUID string, spec ArtifactSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if taskUUID == "" || spec.StepName == "" || spec.Kind == "" || spec.RelativePath == "" || spec.SizeBytes < 0 {
		return fmt.Errorf("artifact record is incomplete")
	}
	if err := validateArtifactPath(spec.RelativePath); err != nil {
		return err
	}
	metadataJSON, err := encodeOptionalJSON(spec.Metadata)
	if err != nil {
		return fmt.Errorf("encode artifact metadata: %w", err)
	}
	relativePath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(spec.RelativePath)))
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return err
	}
	data.artifacts[relativePath] = ArtifactRecord{
		StepName: spec.StepName, Kind: spec.Kind, RelativePath: relativePath,
		SizeBytes: spec.SizeBytes, Committed: spec.Committed, MetadataJSON: metadataJSON,
	}
	return nil
}

func (d *DB) DeleteArtifact(ctx context.Context, taskUUID, relativePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateArtifactPath(relativePath); err != nil {
		return err
	}
	relativePath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return err
	}
	delete(data.artifacts, relativePath)
	return nil
}

func (d *DB) ListArtifacts(ctx context.Context, taskUUID string) ([]ArtifactRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return nil, err
	}
	result := make([]ArtifactRecord, 0, len(data.artifacts))
	for _, artifact := range data.artifacts {
		result = append(result, artifact)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RelativePath < result[j].RelativePath })
	return result, nil
}

func (d *DB) AddWarning(ctx context.Context, taskUUID string, spec WarningSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if taskUUID == "" || spec.Code == "" || spec.Message == "" {
		return fmt.Errorf("warning record is incomplete")
	}
	detailsJSON, err := encodeOptionalJSON(spec.Details)
	if err != nil {
		return fmt.Errorf("encode warning details: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return err
	}
	data.warnings = append(data.warnings, WarningRecord{
		StepName: spec.StepName, Code: spec.Code, Message: spec.Message,
		DetailsJSON: detailsJSON,
	})
	return nil
}

func (d *DB) ListWarnings(ctx context.Context, taskUUID string) ([]WarningRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return nil, err
	}
	return append([]WarningRecord(nil), data.warnings...), nil
}

func (d *DB) ListStepCommands(ctx context.Context, taskUUID string) ([]StepCommandRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return nil, err
	}
	return append([]StepCommandRecord(nil), data.commands...), nil
}

func (d *DB) SnapshotTask(ctx context.Context, taskUUID string) (TaskCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return TaskCheckpoint{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return TaskCheckpoint{}, err
	}
	var request task.Request
	if err := json.Unmarshal([]byte(data.requestJSON), &request); err != nil {
		return TaskCheckpoint{}, fmt.Errorf("decode task request: %w", err)
	}
	checkpoint := TaskCheckpoint{
		Request: request, Task: data.record,
		Warnings: append([]WarningRecord(nil), data.warnings...),
		Commands: append([]StepCommandRecord(nil), data.commands...),
	}
	checkpoint.Steps = make([]StepRecord, 0, len(data.steps))
	for _, step := range data.steps {
		checkpoint.Steps = append(checkpoint.Steps, cloneStepRecord(step.record))
	}
	checkpoint.Artifacts = make([]ArtifactRecord, 0, len(data.artifacts))
	for _, artifact := range data.artifacts {
		checkpoint.Artifacts = append(checkpoint.Artifacts, artifact)
	}
	sort.Slice(checkpoint.Artifacts, func(i, j int) bool {
		return checkpoint.Artifacts[i].RelativePath < checkpoint.Artifacts[j].RelativePath
	})
	return checkpoint, nil
}

func (d *DB) RestoreTask(ctx context.Context, checkpoint TaskCheckpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(checkpoint.Request)
	if err != nil {
		return fmt.Errorf("encode checkpoint request: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := d.taskLocked(checkpoint.Task.TaskUUID)
	if err != nil {
		return err
	}
	if data.requestJSON != string(payload) {
		return task.NewError(task.ErrTaskOutputConflict, "pipeline checkpoint belongs to a different request", false)
	}

	recovered := make(map[string]bool, len(checkpoint.Steps))
	for _, saved := range checkpoint.Steps {
		entry := data.stepsByName[saved.Name]
		if entry == nil || entry.record.Kind != saved.Kind {
			return fmt.Errorf("checkpoint step %s does not match the current plan", saved.Name)
		}
		stepState := task.StepState(saved.State)
		if stepState != task.StepSucceeded && stepState != task.StepSkipped {
			return fmt.Errorf("checkpoint step %s is not recoverable from state %s", saved.Name, saved.State)
		}
		recovered[saved.Name] = true
	}
	for _, artifact := range checkpoint.Artifacts {
		if !recovered[artifact.StepName] {
			return fmt.Errorf("checkpoint artifact %s belongs to an unrecovered step", artifact.RelativePath)
		}
		if err := validateArtifactPath(artifact.RelativePath); err != nil {
			return err
		}
	}

	data.record = checkpoint.Task
	data.record.TaskUUID = checkpoint.Request.TaskUUID
	data.record.Progress = 0
	data.artifacts = make(map[string]ArtifactRecord, len(checkpoint.Artifacts))
	data.warnings = nil
	data.commands = nil
	for _, entry := range data.steps {
		entry.record = StepRecord{
			Name: entry.record.Name, Kind: entry.record.Kind, State: string(task.StepPending),
			MaxAttempts: entry.record.MaxAttempts,
		}
		entry.retryAt = nil
	}
	for _, saved := range checkpoint.Steps {
		entry := data.stepsByName[saved.Name]
		maxAttempts := entry.record.MaxAttempts
		entry.record = cloneStepRecord(saved)
		entry.record.MaxAttempts = maxAttempts
		if entry.record.Attempt > maxAttempts {
			entry.record.Attempt = maxAttempts
		}
	}
	for _, artifact := range checkpoint.Artifacts {
		relativePath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(artifact.RelativePath)))
		artifact.RelativePath = relativePath
		data.artifacts[relativePath] = artifact
	}
	for _, warning := range checkpoint.Warnings {
		if recovered[warning.StepName] {
			data.warnings = append(data.warnings, warning)
		}
	}
	for _, command := range checkpoint.Commands {
		if recovered[command.StepName] {
			data.commands = append(data.commands, command)
		}
	}
	recalculateTaskProgress(data)
	if data.record.State == task.StateSucceeded {
		data.record.Progress = 100
	}
	return nil
}

func (d *DB) taskLocked(taskUUID string) (*taskData, error) {
	data := d.tasks[taskUUID]
	if data == nil {
		return nil, fmt.Errorf("task %s does not exist", taskUUID)
	}
	return data, nil
}

func (d *DB) stepLocked(taskUUID, name string) (*taskData, *stepEntry, error) {
	data, err := d.taskLocked(taskUUID)
	if err != nil {
		return nil, nil, err
	}
	step := data.stepsByName[name]
	if step == nil {
		return nil, nil, fmt.Errorf("step %s does not exist", name)
	}
	return data, step, nil
}

func recalculateTaskProgress(data *taskData) {
	var weighted, total float64
	for _, step := range data.steps {
		total += step.weight
		switch task.StepState(step.record.State) {
		case task.StepSucceeded, task.StepSkipped:
			weighted += step.weight
		case task.StepRunning:
			weighted += step.weight * step.record.Progress / 100
		}
	}
	progress := 0.0
	if total > 0 {
		progress = weighted / total * 100
	}
	if progress > data.record.Progress {
		data.record.Progress = progress
	}
}

func dependenciesSatisfied(data *taskData, dependencies []string) bool {
	for _, dependency := range dependencies {
		step := data.stepsByName[dependency]
		if step == nil {
			return false
		}
		state := task.StepState(step.record.State)
		if state != task.StepSucceeded && state != task.StepSkipped {
			return false
		}
	}
	return true
}

func stepMayRun(step *stepEntry, now time.Time) bool {
	switch task.StepState(step.record.State) {
	case task.StepPending:
		return true
	case task.StepRetryWait:
		return step.retryAt == nil || !step.retryAt.After(now)
	default:
		return false
	}
}

func cloneStepRecord(record StepRecord) StepRecord {
	record.StartedAt = cloneTime(record.StartedAt)
	record.FinishedAt = cloneTime(record.FinishedAt)
	return record
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func encodeOptionalJSON(payload any) (string, error) {
	if payload == nil {
		return "", nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode payload: %w", err)
	}
	return string(data), nil
}

func validateArtifactPath(path string) error {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return fmt.Errorf("artifact path must be a non-empty relative path")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("artifact path escapes task root")
	}
	return nil
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
