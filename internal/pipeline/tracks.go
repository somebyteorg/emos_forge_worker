package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func (e *Executor) selectAudio(ctx context.Context, request task.Request, step state.StepRecord) error {
	probe, err := e.loadProbe(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	selections, err := media.SelectAudioTracks(probe.AudioStreams, media.AudioSelectionOptions{
		Strategy: request.Steps.Audio.Strategy, Languages: request.Steps.Audio.Languages,
		IncludeCommentary: request.Steps.Audio.IncludeCommentary, IncludeVisualImpaired: request.Steps.Audio.IncludeVisualImpaired,
		CopyAll: true,
	})
	if err != nil {
		return task.NewError(task.ErrNoPlayableAudio, err.Error(), false)
	}
	if len(selections) == 0 {
		return task.NewError(task.ErrNoPlayableAudio, "no audio tracks matched the selection policy", false)
	}
	var compatibilityFallbacks []media.AudioSelection
	for index := range selections {
		selection := selections[index]
		if media.CanCopyAudioToHLS(selection.Source.Codec) {
			continue
		}
		fallback := media.NewAACAudioSelection(selection.Source, e.opt.AudioChannels)
		selections[index] = fallback
		compatibilityFallbacks = append(compatibilityFallbacks, fallback)
	}
	e.setStepProgress(request.TaskUUID, step.Name, 5)
	outputs := make([]media.AudioTranscodeOutput, 0, len(selections))
	artifactSpecs := make([]state.ArtifactSpec, 0, len(selections))
	for _, selection := range selections {
		relativePath := filepath.ToSlash(filepath.Join("tmp", "audio", audioOutputName(selection)))
		outputPath := filepath.Join(taskRoot(request), filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return task.NewError(task.ErrAudioTranscodeFailed, err.Error(), true)
		}
		outputs = append(outputs, media.AudioTranscodeOutput{Output: outputPath, Selection: selection})
		artifactSpecs = append(artifactSpecs, state.ArtifactSpec{
			StepName: step.Name, Kind: "audio_intermediate", RelativePath: relativePath, Committed: true, Metadata: selection,
		})
	}
	args, err := media.BuildAudioTranscodeManyArgs(media.AudioTranscodeManySpec{Input: preparedInputPath(request), Outputs: outputs, Threads: e.opt.CPULimit})
	if err != nil {
		return task.NewError(task.ErrAudioTranscodeFailed, err.Error(), false)
	}
	summary := audioSelectCommandSummary(e.opt.FFmpegPath, args, outputs)
	if err := e.runCommandWithProgressSummary(ctx, request.TaskUUID, step.Name, e.opt.FFmpegPath, args, summary, task.ErrAudioTranscodeFailed, ffmpegCommandProgress(5, 92, probe.Format.Duration)); err != nil {
		return err
	}
	if err := e.recordArtifactSpecs(ctx, request, artifactSpecs); err != nil {
		return err
	}
	for _, fallback := range compatibilityFallbacks {
		_ = e.repo.AddWarning(ctx, request.TaskUUID, state.WarningSpec{
			StepName: step.Name, Code: "AUDIO_TRANSCODED_FOR_HLS_COMPATIBILITY",
			Message: "source audio codec is not supported by the MP4/HLS output and was transcoded to AAC",
			Details: map[string]any{
				"source_track_index": fallback.Source.Index,
				"source_codec":       media.NormalizeAudioCodec(fallback.Source.Codec),
				"output_codec":       fallback.OutputCodec,
				"output_profile":     fallback.OutputProfile,
				"output_channels":    fallback.OutputChannels,
				"output_bitrate":     fallback.OutputBitrate,
			},
		})
	}
	e.setStepProgress(request.TaskUUID, step.Name, 96)
	return nil
}

func audioSelectCommandSummary(ffmpegPath string, args []string, outputs []media.AudioTranscodeOutput) string {
	parts := make([]string, 0, len(outputs))
	for _, output := range outputs {
		selection := output.Selection
		language := strings.TrimSpace(selection.Source.Language)
		if language == "" {
			language = "und"
		}
		codec := strings.TrimSpace(audioOutputCodec(selection))
		if codec == "" {
			codec = "unknown"
		}
		mode := "encode"
		if selection.Copy {
			mode = "copy"
		}
		parts = append(parts, fmt.Sprintf("%s %s %s", language, codec, mode))
	}
	trackWord := "tracks"
	if len(outputs) == 1 {
		trackWord = "track"
	}
	return fmt.Sprintf("audio_select %d %s | %s | %s", len(outputs), trackWord, strings.Join(parts, ", "), commandSummary(ffmpegPath, args))
}

func (e *Executor) transcodeAudioAAC(ctx context.Context, request task.Request, step state.StepRecord) error {
	probe, err := e.loadProbe(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	artifacts, err := e.repo.ListArtifacts(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	sources, err := selectedAudioArtifactInputs(artifacts)
	if err != nil {
		return task.NewError(task.ErrAudioTranscodeFailed, err.Error(), false)
	}
	if len(sources) == 0 {
		return task.NewError(task.ErrAudioTranscodeFailed, "no selected audio tracks are available for AAC conversion", false)
	}
	if err := e.deleteStepArtifacts(ctx, request, step.Name); err != nil {
		return task.NewError(task.ErrAudioTranscodeFailed, err.Error(), true)
	}
	var outputs []media.AudioFileTranscodeOutput
	var artifactSpecs []state.ArtifactSpec
	for _, source := range sources {
		if audioOutputCodec(source.Selection) == "aac" {
			continue
		}
		selection := media.NewAACAudioSelection(source.Selection.Source, e.opt.AudioChannels)
		relativePath := filepath.ToSlash(filepath.Join("tmp", "audio", audioOutputName(selection)))
		outputPath := filepath.Join(taskRoot(request), filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return task.NewError(task.ErrAudioTranscodeFailed, err.Error(), true)
		}
		outputs = append(outputs, media.AudioFileTranscodeOutput{
			Input:     filepath.Join(taskRoot(request), filepath.FromSlash(source.Artifact.RelativePath)),
			Output:    outputPath,
			Selection: selection,
		})
		artifactSpecs = append(artifactSpecs, state.ArtifactSpec{
			StepName: step.Name, Kind: "audio_aac_intermediate", RelativePath: relativePath, Committed: true, Metadata: selection,
		})
	}
	if len(outputs) == 0 {
		return &stepSkippedError{Details: map[string]any{"reason": "selected audio tracks are already aac"}}
	}
	e.setStepProgress(request.TaskUUID, step.Name, 5)
	args, err := media.BuildAudioFileTranscodeManyArgs(media.AudioFileTranscodeManySpec{Outputs: outputs, Threads: e.opt.CPULimit})
	if err != nil {
		return task.NewError(task.ErrAudioTranscodeFailed, err.Error(), false)
	}
	summary := audioAACBatchCommandSummary(e.opt.FFmpegPath, args, outputs)
	if err := e.runCommandWithProgressSummary(ctx, request.TaskUUID, step.Name, e.opt.FFmpegPath, args, summary, task.ErrAudioTranscodeFailed, ffmpegCommandProgress(5, 92, probe.Format.Duration)); err != nil {
		return err
	}
	if err := e.recordArtifactSpecs(ctx, request, artifactSpecs); err != nil {
		return err
	}
	e.setStepProgress(request.TaskUUID, step.Name, 96)
	return nil
}

func audioAACBatchCommandSummary(ffmpegPath string, args []string, outputs []media.AudioFileTranscodeOutput) string {
	parts := make([]string, 0, len(outputs))
	for _, output := range outputs {
		language := strings.TrimSpace(output.Selection.Source.Language)
		if language == "" {
			language = "und"
		}
		sourceCodec := media.NormalizeAudioCodec(output.Selection.Source.Codec)
		if sourceCodec == "" {
			sourceCodec = "unknown"
		}
		outputCodec := media.NormalizeAudioCodec(output.Selection.OutputCodec)
		if outputCodec == "" {
			outputCodec = "aac"
		}
		parts = append(parts, fmt.Sprintf("%s %s -> %s", language, sourceCodec, outputCodec))
	}
	trackWord := "tracks"
	if len(outputs) == 1 {
		trackWord = "track"
	}
	return fmt.Sprintf("audio_aac batch %d %s | %s | %s", len(outputs), trackWord, strings.Join(parts, ", "), commandSummary(ffmpegPath, args))
}

type selectedAudioArtifactInput struct {
	Artifact  state.ArtifactRecord
	Selection media.AudioSelection
}

func selectedAudioArtifactInputs(artifacts []state.ArtifactRecord) ([]selectedAudioArtifactInput, error) {
	var result []selectedAudioArtifactInput
	for _, artifact := range artifacts {
		if !artifact.Committed || artifact.Kind != "audio_intermediate" {
			continue
		}
		selection, err := audioSelectionFromArtifact(artifact)
		if err != nil {
			return nil, err
		}
		result = append(result, selectedAudioArtifactInput{Artifact: artifact, Selection: selection})
	}
	return result, nil
}

func selectedAudioArtifacts(artifacts []state.ArtifactRecord) ([]media.AudioSelection, error) {
	sources, err := selectedAudioArtifactInputs(artifacts)
	if err != nil {
		return nil, err
	}
	result := make([]media.AudioSelection, 0, len(sources))
	for _, source := range sources {
		result = append(result, source.Selection)
	}
	return result, nil
}

func (e *Executor) extractSubtitles(ctx context.Context, request task.Request, step state.StepRecord) error {
	probe, err := e.loadProbe(ctx, request.TaskUUID)
	if err != nil {
		return err
	}
	if err := e.deleteStepArtifacts(ctx, request, step.Name); err != nil {
		return task.NewError(task.ErrSubtitleConvertFailed, err.Error(), true)
	}
	if err := removeTaskRelativeDir(request, "subtitles"); err != nil {
		return task.NewError(task.ErrSubtitleConvertFailed, err.Error(), true)
	}
	var subtitles []media.SubtitleStream
	for _, subtitle := range probe.Subtitles {
		if !media.IsTextSubtitleCodec(subtitle.Codec) {
			_ = e.repo.AddWarning(ctx, request.TaskUUID, state.WarningSpec{
				StepName: step.Name, Code: "UNSUPPORTED_IMAGE_SUBTITLE", Message: "image subtitle was skipped",
				Details: map[string]any{"codec": subtitle.Codec, "language": subtitle.Language, "source_track_index": subtitle.Index},
			})
			continue
		}
		subtitles = append(subtitles, subtitle)
	}
	if len(subtitles) == 0 && request.Steps.Subtitles.Required {
		return task.NewError(task.ErrUnsupportedMedia, "no text subtitle tracks are available", false)
	}
	if len(subtitles) == 0 {
		e.setStepProgress(request.TaskUUID, step.Name, 96)
		return nil
	}
	e.setStepProgress(request.TaskUUID, step.Name, 10)
	outputs := make([]media.SubtitleConvertOutput, 0, len(subtitles))
	artifactSpecs := make([]state.ArtifactSpec, 0, len(subtitles))
	for _, subtitle := range subtitles {
		relativePath := filepath.ToSlash(filepath.Join("subtitles", subtitleOutputName(subtitle)))
		outputPath := filepath.Join(taskRoot(request), filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return task.NewError(task.ErrSubtitleConvertFailed, err.Error(), true)
		}
		outputs = append(outputs, media.SubtitleConvertOutput{Output: outputPath, SourceIndex: subtitle.Index, Codec: subtitle.Codec})
		artifactSpecs = append(artifactSpecs, state.ArtifactSpec{
			StepName: step.Name, Kind: "subtitle", RelativePath: relativePath, Committed: true, Metadata: subtitle,
		})
	}
	args, err := media.BuildSubtitleConvertManyArgs(media.SubtitleConvertManySpec{Input: preparedInputPath(request), Outputs: outputs, Threads: e.opt.CPULimit})
	if err != nil {
		return task.NewError(task.ErrSubtitleConvertFailed, err.Error(), false)
	}
	if err := e.runCommandWithProgress(ctx, request.TaskUUID, step.Name, e.opt.FFmpegPath, args, task.ErrSubtitleConvertFailed, ffmpegCommandProgress(10, 92, probe.Format.Duration)); err != nil {
		return err
	}
	if err := e.recordArtifactSpecs(ctx, request, artifactSpecs); err != nil {
		return err
	}
	e.setStepProgress(request.TaskUUID, step.Name, 96)
	return nil
}

func audioOutputCodec(selection media.AudioSelection) string {
	if strings.TrimSpace(selection.OutputCodec) != "" {
		return media.NormalizeAudioCodec(selection.OutputCodec)
	}
	return media.NormalizeAudioCodec(selection.Source.Codec)
}

func audioOutputName(selection media.AudioSelection) string {
	language := safeFileSegment(selection.Source.Language)
	if language == "" {
		language = "und"
	}
	codec := safeFileSegment(audioOutputCodec(selection))
	if codec == "" {
		codec = "aac"
	}
	extension := ".m4a"
	if selection.Copy && codec != "aac" {
		extension = ".mp4"
	}
	return fmt.Sprintf("audio_%02d_%s_%s%s", selection.Source.Index, language, codec, extension)
}

func subtitleOutputName(subtitle media.SubtitleStream) string {
	language := safeFileSegment(subtitle.Language)
	if language == "" {
		language = "und"
	}
	return fmt.Sprintf("sub_%02d_%s.vtt", subtitle.Index, language)
}
