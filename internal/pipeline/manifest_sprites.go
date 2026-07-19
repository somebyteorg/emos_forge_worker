package pipeline

import (
	"encoding/json"
	"fmt"
	"sort"

	"forge_worker/internal/manifest"
	"forge_worker/internal/state"
)

type manifestSpriteArtifact struct {
	path     string
	size     int64
	metadata spriteMetadata
}

func manifestSprites(artifacts []state.ArtifactRecord) ([]manifest.SpriteSheet, error) {
	type spriteGroup struct {
		mediaID    string
		cellWidth  int
		cellHeight int
		columns    int
		rows       int
		items      []manifestSpriteArtifact
	}
	groups := make(map[string]*spriteGroup)
	for _, artifact := range artifacts {
		if !artifact.Committed || artifact.Kind != "sprite" {
			continue
		}
		var metadata spriteMetadata
		if err := json.Unmarshal([]byte(artifact.MetadataJSON), &metadata); err != nil {
			return nil, fmt.Errorf("decode sprite metadata: %w", err)
		}
		if metadata.CellWidth <= 0 || metadata.CellHeight <= 0 || metadata.Columns <= 0 || metadata.Rows <= 0 {
			return nil, fmt.Errorf("sprite metadata is incomplete for %s", artifact.RelativePath)
		}
		if metadata.FrameCount > 0 && len(metadata.TimestampsSeconds) != metadata.FrameCount {
			return nil, fmt.Errorf("sprite %s frame_count does not match timestamps", artifact.RelativePath)
		}
		key := fmt.Sprintf("%dx%d:%d", metadata.CellWidth, metadata.CellHeight, metadata.Columns)
		group := groups[key]
		if group == nil {
			group = &spriteGroup{mediaID: firstNonEmpty(metadata.MediaID, spriteMediaIDForSize(metadata.CellWidth, metadata.CellHeight)), cellWidth: metadata.CellWidth, cellHeight: metadata.CellHeight, columns: metadata.Columns}
			groups[key] = group
		}
		rows := metadata.GridRows
		if rows <= 0 {
			rows = metadata.Rows
		}
		group.rows = max(group.rows, rows)
		group.items = append(group.items, manifestSpriteArtifact{path: artifact.RelativePath, size: artifact.SizeBytes, metadata: metadata})
	}
	result := make([]manifest.SpriteSheet, 0, len(groups))
	for _, group := range groups {
		sort.Slice(group.items, func(i, j int) bool {
			if group.items[i].metadata.FrameStart == group.items[j].metadata.FrameStart {
				return group.items[i].path < group.items[j].path
			}
			return group.items[i].metadata.FrameStart < group.items[j].metadata.FrameStart
		})
		sprite := manifest.SpriteSheet{
			MediaID: group.mediaID, Width: group.cellWidth, Height: group.cellHeight, Columns: group.columns, Rows: group.rows,
		}
		for _, item := range group.items {
			sprite.Images = append(sprite.Images, item.path)
			sprite.FileSize += item.size
			sprite.FrameTimes = append(sprite.FrameTimes, item.metadata.TimestampsSeconds...)
		}
		sprite.CountFrame = len(sprite.FrameTimes)
		result = append(result, sprite)
	}
	return result, nil
}
