package pipeline

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"forge_worker/internal/manifest"
	"forge_worker/internal/media"
	"forge_worker/internal/state"
	"forge_worker/internal/task"
)

func sourceManifest(probe media.Probe, request task.Request) map[string]any {
	return map[string]any{
		"type":             request.Input.Type,
		"duration_seconds": probe.Format.Duration,
		"size_bytes":       probe.Format.SizeBytes,
		"format":           probe.Format.Name,
	}
}

func processingManifest(record state.TaskRecord, completedAt time.Time, steps []state.StepRecord, commands []state.StepCommandRecord, artifacts []state.ArtifactRecord, opt Options) map[string]any {
	processing := map[string]any{
		"worker":          "forge_worker",
		"encryption_mode": normalizedEncryptionMode(opt.EncryptionMode),
	}
	if !record.CreatedAt.IsZero() {
		processing["elapsed_seconds"] = seconds(completedAt.Sub(record.CreatedAt))
	}
	commandsByStep := make(map[string][]string)
	for _, command := range commands {
		if command.Summary == "" {
			continue
		}
		commandsByStep[command.StepName] = append(commandsByStep[command.StepName], command.Summary)
	}
	stepItems := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		item := map[string]any{
			"name":     ExternalStepName(step.Name),
			"kind":     step.Kind,
			"state":    step.State,
			"attempt":  step.Attempt,
			"progress": step.Progress,
		}
		if step.FPS > 0 {
			item["fps"] = step.FPS
		}
		if step.Speed > 0 {
			item["speed"] = step.Speed
		}
		if step.DetailsJSON != "" {
			var details any
			if err := json.Unmarshal([]byte(step.DetailsJSON), &details); err == nil {
				item["details"] = details
			}
		}
		stepCommands := commandsByStep[step.Name]
		if len(stepCommands) == 0 && step.CommandSummary != "" {
			stepCommands = []string{step.CommandSummary}
		}
		if len(stepCommands) > 0 {
			item["commands"] = stepCommands
		}
		if step.StartedAt != nil {
			item["started_at"] = step.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if step.FinishedAt != nil {
			item["finished_at"] = step.FinishedAt.UTC().Format(time.RFC3339Nano)
		}
		if step.StartedAt != nil && step.FinishedAt != nil {
			item["duration_seconds"] = seconds(step.FinishedAt.Sub(*step.StartedAt))
		}
		stepItems = append(stepItems, item)
	}
	processing["steps"] = stepItems

	bytesByKind := make(map[string]int64)
	countByKind := make(map[string]int)
	totalBytes := int64(0)
	for _, artifact := range artifacts {
		if !deliverableArtifact(artifact) {
			continue
		}
		bytesByKind[artifact.Kind] += artifact.SizeBytes
		countByKind[artifact.Kind]++
		totalBytes += artifact.SizeBytes
	}
	processing["artifact_bytes_total"] = totalBytes
	processing["artifact_bytes_by_kind"] = bytesByKind
	processing["artifact_count_by_kind"] = countByKind
	return processing
}

func playbackManifest(request task.Request, opt Options) manifest.Playback {
	if !hasAVRequest(request) {
		return manifest.Playback{}
	}
	encryptionInfo := manifest.Encryption{Scheme: normalizedEncryptionMode(opt.EncryptionMode)}
	if encryptionInfo.Scheme == media.PackageEncryptionClearKey {
		encryptionInfo = manifest.Encryption{Scheme: "cbcs", KeySystem: "clearkey"}
	}
	return manifest.Playback{
		HLSMaster: "master.m3u8", Container: "cmaf", SegmentFormat: "fmp4",
		Encryption:           encryptionInfo,
		SegmentTargetSeconds: opt.SegmentTarget.Seconds(), SegmentMaxSeconds: opt.SegmentMax.Seconds(),
	}
}

func deliverableArtifact(artifact state.ArtifactRecord) bool {
	return artifact.Committed && artifact.Kind != "keys" && !strings.HasPrefix(artifact.RelativePath, "tmp/")
}

func manifestWarnings(records []state.WarningRecord) []manifest.Warning {
	result := make([]manifest.Warning, 0, len(records))
	for _, record := range records {
		warning := manifest.Warning{Code: record.Code, Step: record.StepName, Message: record.Message}
		if record.DetailsJSON != "" {
			var details map[string]any
			if err := json.Unmarshal([]byte(record.DetailsJSON), &details); err == nil {
				warning.Details = details
			}
		}
		result = append(result, warning)
	}
	return result
}

func sortManifest(m *manifest.Manifest) {
	sort.Slice(m.VideoTracks, func(i, j int) bool { return m.VideoTracks[i].MediaID < m.VideoTracks[j].MediaID })
	sort.Slice(m.AudioTracks, func(i, j int) bool { return m.AudioTracks[i].MediaID < m.AudioTracks[j].MediaID })
	sort.Slice(m.Subtitles, func(i, j int) bool { return m.Subtitles[i].Path < m.Subtitles[j].Path })
	sort.Slice(m.Sprites, func(i, j int) bool {
		if m.Sprites[i].Width == m.Sprites[j].Width {
			return m.Sprites[i].Height > m.Sprites[j].Height
		}
		return m.Sprites[i].Width > m.Sprites[j].Width
	})
	for i := range m.VideoTracks {
		sort.Slice(m.VideoTracks[i].Segments, func(j, k int) bool {
			return m.VideoTracks[i].Segments[j].Sequence < m.VideoTracks[i].Segments[k].Sequence
		})
	}
	for i := range m.AudioTracks {
		sort.Slice(m.AudioTracks[i].Segments, func(j, k int) bool {
			return m.AudioTracks[i].Segments[j].Sequence < m.AudioTracks[i].Segments[k].Sequence
		})
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
