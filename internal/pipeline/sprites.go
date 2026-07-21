package pipeline

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"

	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func (e *Executor) generateSprites(ctx context.Context, request task.Request, step state.StepRecord) error {
	probe, err := e.loadProbe(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	source, ok := primaryVideoStream(probe.VideoStreams)
	if !ok {
		return task.NewError(task.ErrUnsupportedMedia, "input has no video stream", false)
	}
	framesPerSheet := request.Steps.Sprites.Columns * request.Steps.Sprites.Rows
	if framesPerSheet <= 0 {
		return task.NewError(task.ErrInvalidTaskSchema, "sprite columns and rows must be positive", false)
	}
	duration := probe.Format.Duration
	if duration <= 0 {
		return task.NewError(task.ErrSpriteGenerationFailed, "input duration is required for sprite generation", false)
	}
	sizes, err := parseSpriteSizes(request.Steps.Sprites.Sizes)
	if err != nil {
		return task.NewError(task.ErrInvalidTaskSchema, err.Error(), false)
	}
	if err := e.cleanupSpriteAttempt(ctx, request, step.Name, sizes); err != nil {
		return err
	}
	e.setStepProgress(request.TaskUUID, step.Name, 4)
	spriteInputs, err := e.selectSpriteInputs(ctx, request, source)
	if err != nil {
		return err
	}
	interval := seconds(generatedVideoSegmentDuration)
	var selectedInput spriteInput
	var frames []selectedSpriteFrame
	keyframeFailures := make([]map[string]any, 0, len(spriteInputs))
	for _, candidate := range spriteInputs {
		frames, err = e.spriteKeyframes(ctx, candidate.Path, candidate.SourceIndex, duration, interval)
		if err == nil {
			selectedInput = candidate
			break
		}
		if errors.Is(err, errNoSpriteKeyframes) {
			keyframeFailures = append(keyframeFailures, spriteInputDetails(candidate))
			continue
		}
		return taskErrorWithDetails(task.ErrSpriteGenerationFailed, err.Error(), false, spriteErrorDetails(candidate, duration, interval, nil))
	}
	if len(frames) == 0 {
		return taskErrorWithDetails(task.ErrSpriteGenerationFailed, errNoSpriteKeyframes.Error(), false, spriteErrorDetails(spriteInput{}, duration, interval, keyframeFailures))
	}
	e.setStepProgress(request.TaskUUID, step.Name, 10)
	return e.generateSpritesFromKeyframes(ctx, request, step, selectedInput, sizes, frames, interval, framesPerSheet)
}

type spriteInput struct {
	Path         string
	SourceIndex  int
	DynamicRange media.DynamicRange
	Mode         string
	Profile      string
}

func (e *Executor) selectSpriteInputs(ctx context.Context, request task.Request, source media.VideoStream) ([]spriteInput, error) {
	artifacts, err := e.repo.ListArtifacts(ctx, request.TaskUUID)
	if err != nil {
		return nil, err
	}
	byProfile := make(map[string]spriteInput)
	var artifactInputs []spriteInput
	for _, artifact := range artifacts {
		if !artifact.Committed || artifact.Kind != "video_intermediate" {
			continue
		}
		metadata, err := videoIntermediateMetadataFromArtifact(artifact)
		if err != nil {
			return nil, err
		}
		input := spriteInput{
			Path:         filepath.Join(taskRoot(request), filepath.FromSlash(artifact.RelativePath)),
			SourceIndex:  0,
			DynamicRange: metadata.Profile.DynamicRange,
			Mode:         metadata.Mode,
			Profile:      metadata.Profile.Name,
		}
		byProfile[metadata.Profile.Name] = input
		artifactInputs = append(artifactInputs, input)
	}
	candidates := make([]spriteInput, 0, len(artifactInputs)+1)
	seen := make(map[string]bool, len(artifactInputs)+1)
	for _, profile := range []string{"720p", "1080p", "package"} {
		if input, ok := byProfile[profile]; ok {
			appendSpriteInputCandidate(&candidates, seen, input)
		}
	}
	sort.SliceStable(artifactInputs, func(i, j int) bool {
		if artifactInputs[i].Profile == artifactInputs[j].Profile {
			return artifactInputs[i].Path < artifactInputs[j].Path
		}
		return artifactInputs[i].Profile < artifactInputs[j].Profile
	})
	for _, input := range artifactInputs {
		appendSpriteInputCandidate(&candidates, seen, input)
	}
	appendSpriteInputCandidate(&candidates, seen, spriteInput{
		Path:         preparedInputPath(request),
		SourceIndex:  source.Index,
		DynamicRange: spriteSourceDynamicRange(source),
		Mode:         "source",
		Profile:      "",
	})
	return candidates, nil
}

func spriteSourceDynamicRange(source media.VideoStream) media.DynamicRange {
	if processingSource, ok := media.VideoStreamForProcessing(source); ok {
		return processingSource.DynamicRange
	}
	return source.DynamicRange
}

func appendSpriteInputCandidate(candidates *[]spriteInput, seen map[string]bool, input spriteInput) {
	key := fmt.Sprintf("%s#%d", input.Path, input.SourceIndex)
	if seen[key] {
		return
	}
	seen[key] = true
	*candidates = append(*candidates, input)
}

func spriteErrorDetails(input spriteInput, duration, interval float64, candidates []map[string]any) map[string]any {
	details := map[string]any{
		"duration_seconds":    duration,
		"interval_seconds":    interval,
		"start_after_seconds": spriteStartAfterSeconds,
	}
	if input.Path != "" {
		for key, value := range spriteInputDetails(input) {
			details[key] = value
		}
	}
	if len(candidates) > 0 {
		details["candidate_inputs"] = candidates
	}
	return details
}

func spriteInputDetails(input spriteInput) map[string]any {
	return map[string]any{
		"input":              input.Path,
		"source_track_index": input.SourceIndex,
		"input_mode":         input.Mode,
		"input_profile":      input.Profile,
	}
}

func (e *Executor) cleanupSpriteAttempt(ctx context.Context, request task.Request, stepName string, sizes []spriteSize) error {
	if err := e.deleteStepArtifacts(ctx, request, stepName); err != nil {
		return task.NewError(task.ErrSpriteGenerationFailed, err.Error(), true)
	}
	if err := removeTaskRelativeDir(request, filepath.ToSlash(filepath.Join("tmp", "sprites", "masters"))); err != nil {
		return task.NewError(task.ErrSpriteGenerationFailed, err.Error(), true)
	}
	for _, size := range sizes {
		if err := removeTaskRelativeDir(request, filepath.ToSlash(filepath.Join("sprites", size.Name))); err != nil {
			return task.NewError(task.ErrSpriteGenerationFailed, err.Error(), true)
		}
	}
	return nil
}

func (e *Executor) generateSpritesFromKeyframes(ctx context.Context, request task.Request, step state.StepRecord, input spriteInput, sizes []spriteSize, frames []selectedSpriteFrame, interval float64, framesPerSheet int) error {
	frameTotal := len(frames)
	sheetTotal := (frameTotal + framesPerSheet - 1) / framesPerSheet
	frameFormat := spriteFrameFormat(request)
	progress := newSpriteCommandProgress(10, 94, spriteCommandCount(spriteSizeGroups(sizes), sheetTotal))
	for _, group := range spriteSizeGroups(sizes) {
		master := group[0]
		for sheetIndex := 0; sheetIndex < sheetTotal; sheetIndex++ {
			frameStart := sheetIndex * framesPerSheet
			frameCount := min(framesPerSheet, frameTotal-frameStart)
			if frameCount <= 0 {
				continue
			}
			workDir := filepath.Join(taskRoot(request), "tmp", "sprites", "masters", master.Name, fmt.Sprintf("sheet_%04d", sheetIndex+1))
			if err := os.RemoveAll(workDir); err != nil {
				return task.NewError(task.ErrSpriteGenerationFailed, err.Error(), true)
			}
			if err := os.MkdirAll(workDir, 0o700); err != nil {
				return task.NewError(task.ErrSpriteGenerationFailed, err.Error(), true)
			}
			sheetFrames := frames[frameStart : frameStart+frameCount]
			framePattern := filepath.Join(workDir, "frame_%06d."+frameFormat)
			extractArgs, err := media.BuildSpriteKeyframeExtractArgs(media.SpriteKeyframeExtractSpec{
				Input: input.Path, OutputGlob: framePattern, SourceIndex: input.SourceIndex, Width: master.Width, Height: master.Height,
				SeekSecond:       spriteSeekSecond(sheetFrames),
				KeyframeOrdinals: relativeSpriteKeyframeOrdinals(sheetFrames),
				DynamicRange:     input.DynamicRange,
				Threads:          max(1, e.opt.CPULimit),
			})
			if err != nil {
				return task.NewError(task.ErrSpriteGenerationFailed, err.Error(), false)
			}
			if err := e.runCommandWithProgress(ctx, request.TaskUUID, step.Name, e.opt.FFmpegPath, extractArgs, task.ErrSpriteGenerationFailed, progress.nextStage()); err != nil {
				return err
			}
			inputs := framePathsNumbered(workDir, 1, frameCount, 6, frameFormat)
			specs, err := e.assembleSpriteSheetFromFrames(ctx, request, step.Name, group, inputs, sheetIndex, frameStart, sheetFrames, interval, "keyframe_master", "keyframe_resized", progress)
			if err != nil {
				return err
			}
			if err := e.recordArtifactSpecs(ctx, request, specs); err != nil {
				return err
			}
			_ = os.RemoveAll(workDir)
		}
		_ = os.RemoveAll(filepath.Join(taskRoot(request), "tmp", "sprites", "masters", master.Name))
	}
	e.setStepProgress(request.TaskUUID, step.Name, 98)
	return nil
}

func (e *Executor) generateSpritesFromFrameDirectories(ctx context.Context, request task.Request, stepName string, groups [][]spriteSize, frames []selectedSpriteFrame, interval float64, framesPerSheet int, workDirs map[string]string, masterMode, resizedMode string) ([]state.ArtifactSpec, error) {
	frameTotal := len(frames)
	sheetTotal := (frameTotal + framesPerSheet - 1) / framesPerSheet
	frameFormat := spriteFrameFormat(request)
	var result []state.ArtifactSpec
	for _, group := range groups {
		master := group[0]
		workDir := workDirs[master.Name]
		if workDir == "" {
			return nil, task.NewError(task.ErrSpriteGenerationFailed, "sprite frame directory is missing", true)
		}
		for sheetIndex := 0; sheetIndex < sheetTotal; sheetIndex++ {
			frameStart := sheetIndex * framesPerSheet
			frameCount := min(framesPerSheet, frameTotal-frameStart)
			if frameCount <= 0 {
				continue
			}
			sheetFrames := frames[frameStart : frameStart+frameCount]
			inputs := framePathsNumbered(workDir, frameStart+1, frameCount, 6, frameFormat)
			specs, err := e.assembleSpriteSheetFromFrames(ctx, request, stepName, group, inputs, sheetIndex, frameStart, sheetFrames, interval, masterMode, resizedMode, nil)
			if err != nil {
				return nil, err
			}
			result = append(result, specs...)
		}
	}
	return result, nil
}

func (e *Executor) assembleSpriteSheetFromFrames(ctx context.Context, request task.Request, stepName string, group []spriteSize, inputs []string, sheetIndex, frameStart int, sheetFrames []selectedSpriteFrame, interval float64, masterMode, resizedMode string, progress *spriteCommandProgress) ([]state.ArtifactSpec, error) {
	master := group[0]
	frameCount := len(sheetFrames)
	gridColumns := request.Steps.Sprites.Columns
	gridRows := request.Steps.Sprites.Rows
	if len(inputs) == 0 {
		return nil, task.NewError(task.ErrSpriteGenerationFailed, "sprite grid inputs are required", false)
	}
	paddingInput := filepath.Join(filepath.Dir(inputs[0]), ".padding.png")
	gridInputs, err := padSpriteGridInputs(inputs, gridColumns, gridRows, paddingInput)
	if err != nil {
		return nil, task.NewError(task.ErrSpriteGenerationFailed, err.Error(), false)
	}
	if len(gridInputs) > len(inputs) {
		if err := writeBlackSpriteFrame(paddingInput, master.Width, master.Height); err != nil {
			return nil, task.NewError(task.ErrSpriteGenerationFailed, err.Error(), true)
		}
	}
	masterRelativePath := filepath.ToSlash(filepath.Join("sprites", master.Name, fmt.Sprintf("sprite_%04d.avif", sheetIndex+1)))
	masterOutputPath := filepath.Join(taskRoot(request), filepath.FromSlash(masterRelativePath))
	if err := os.MkdirAll(filepath.Dir(masterOutputPath), 0o700); err != nil {
		return nil, task.NewError(task.ErrSpriteGenerationFailed, err.Error(), true)
	}
	joinArgs, err := media.BuildVipsArrayJoinArgs(media.VipsJoinSpec{Inputs: gridInputs, Output: masterOutputPath, Columns: gridColumns, Quality: request.Steps.Sprites.Quality, Effort: request.Steps.Sprites.Effort})
	if err != nil {
		return nil, task.NewError(task.ErrSpriteGenerationFailed, err.Error(), false)
	}
	if err := e.runCommandWithProgress(ctx, request.TaskUUID, stepName, e.opt.VIPSPath, joinArgs, task.ErrSpriteGenerationFailed, progress.nextPercent()); err != nil {
		return nil, err
	}
	if err := appendForgeUUIDTags(request, []string{masterOutputPath}, task.ErrSpriteGenerationFailed); err != nil {
		return nil, err
	}
	timestamps := spriteTimestamps(sheetFrames)
	specs := []state.ArtifactSpec{spriteArtifactSpec(stepName, master, masterRelativePath, gridColumns, gridRows, frameStart, frameCount, interval, timestamps, masterMode)}
	for _, size := range group[1:] {
		relativePath := filepath.ToSlash(filepath.Join("sprites", size.Name, fmt.Sprintf("sprite_%04d.avif", sheetIndex+1)))
		outputPath := filepath.Join(taskRoot(request), filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return nil, task.NewError(task.ErrSpriteGenerationFailed, err.Error(), true)
		}
		resizeArgs, err := media.BuildVipsResizeArgs(media.VipsResizeSpec{
			Input: masterOutputPath, Output: outputPath, Scale: float64(size.Width) / float64(master.Width),
			Quality: request.Steps.Sprites.Quality, Effort: request.Steps.Sprites.Effort,
		})
		if err != nil {
			return nil, task.NewError(task.ErrSpriteGenerationFailed, err.Error(), false)
		}
		if err := e.runCommandWithProgress(ctx, request.TaskUUID, stepName, e.opt.VIPSPath, resizeArgs, task.ErrSpriteGenerationFailed, progress.nextPercent()); err != nil {
			return nil, err
		}
		if err := appendForgeUUIDTags(request, []string{outputPath}, task.ErrSpriteGenerationFailed); err != nil {
			return nil, err
		}
		specs = append(specs, spriteArtifactSpec(stepName, size, relativePath, gridColumns, gridRows, frameStart, frameCount, interval, timestamps, resizedMode))
	}
	return specs, nil
}

func padSpriteGridInputs(inputs []string, columns, rows int, paddingInput string) ([]string, error) {
	if len(inputs) == 0 || columns <= 0 || rows <= 0 {
		return nil, fmt.Errorf("sprite grid inputs, columns, and rows are required")
	}
	capacity := columns * rows
	if capacity/columns != rows || len(inputs) > capacity {
		return nil, fmt.Errorf("sprite frame count %d exceeds grid capacity %dx%d", len(inputs), columns, rows)
	}
	if len(inputs) < capacity && paddingInput == "" {
		return nil, fmt.Errorf("sprite grid padding input is required")
	}
	padded := make([]string, capacity)
	copy(padded, inputs)
	for index := len(inputs); index < capacity; index++ {
		padded[index] = paddingInput
	}
	return padded, nil
}

func writeBlackSpriteFrame(path string, width, height int) error {
	if path == "" || width <= 0 || height <= 0 {
		return fmt.Errorf("sprite padding path, width, and height are required")
	}
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create sprite padding frame: %w", err)
	}
	if err := png.Encode(file, frame); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("encode sprite padding frame: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close sprite padding frame: %w", err)
	}
	return nil
}

func spriteArtifactSpec(stepName string, size spriteSize, relativePath string, columns, rows, frameStart, frameCount int, interval float64, timestamps []float64, mode string) state.ArtifactSpec {
	metadata := spriteMetadata{
		MediaID: spriteMediaID(size), Path: relativePath, Width: size.Width * columns, Height: size.Height * rows,
		CellWidth: size.Width, CellHeight: size.Height, Columns: columns, Rows: rows, GridRows: rows,
		FrameStart: frameStart, FrameCount: frameCount, FirstTimestampSeconds: timestamps[0],
		LastTimestampSeconds: timestamps[len(timestamps)-1], IntervalSeconds: interval, Mode: mode, TimestampsSeconds: timestamps,
	}
	return state.ArtifactSpec{StepName: stepName, Kind: "sprite", RelativePath: relativePath, Committed: true, Metadata: metadata}
}

func framePathsNumbered(directory string, first, count, digits int, frameFormat string) []string {
	result := make([]string, 0, count)
	format := "frame_%04d." + frameFormat
	if digits == 6 {
		format = "frame_%06d." + frameFormat
	}
	for i := 0; i < count; i++ {
		result = append(result, filepath.Join(directory, fmt.Sprintf(format, first+i)))
	}
	return result
}

func spriteFrameFormat(request task.Request) string {
	switch request.Steps.Sprites.FrameFormat {
	case "ppm":
		return "ppm"
	default:
		return "png"
	}
}
