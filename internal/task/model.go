package task

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

type InputKind string

const (
	InputLocal InputKind = "local"
	InputURL   InputKind = "url"
)

type Request struct {
	TaskUUID string       `json:"task_uuid,omitempty"`
	Input    Input        `json:"input"`
	Output   Output       `json:"output"`
	Steps    StepRequests `json:"steps"`
}

type Input struct {
	Type InputKind `json:"type"`
	URI  string    `json:"uri"`
}

type Output struct {
	Root string `json:"root"`
}

type StepRequests struct {
	Subtitles SubtitleRequest `json:"subtitles"`
	Audio     AudioRequest    `json:"audio"`
	Video     VideoRequest    `json:"video"`
	Sprites   SpriteRequest   `json:"sprites"`
}

type SubtitleRequest struct {
	Enabled  bool `json:"enabled"`
	Required bool `json:"required,omitempty"`
}

type AudioRequest struct {
	Enabled               bool     `json:"enabled"`
	Strategy              string   `json:"strategy,omitempty"`
	Package               bool     `json:"package,omitempty"`
	AAC                   bool     `json:"aac,omitempty"`
	Languages             []string `json:"languages,omitempty"`
	IncludeCommentary     bool     `json:"include_commentary,omitempty"`
	IncludeVisualImpaired bool     `json:"include_visual_impaired,omitempty"`
}

type VideoRequest struct {
	Enabled  bool     `json:"enabled"`
	Profiles []string `json:"profiles,omitempty"`
}

type SpriteRequest struct {
	Enabled     bool     `json:"enabled"`
	Sizes       []string `json:"sizes,omitempty"`
	Columns     int      `json:"columns,omitempty"`
	Rows        int      `json:"rows,omitempty"`
	Quality     int      `json:"quality,omitempty"`
	Effort      int      `json:"effort,omitempty"`
	FrameFormat string   `json:"frame_format,omitempty"`
}

func (r Request) Validate() error {
	if r.TaskUUID != "" && !ValidUUID(r.TaskUUID) {
		return NewError(ErrInvalidTaskSchema, "task_uuid must be a UUID", false)
	}
	if strings.TrimSpace(r.Input.URI) == "" {
		return NewError(ErrInvalidTaskSchema, "input URI is required", false)
	}
	switch r.Input.Type {
	case InputLocal:
		if !filepath.IsAbs(r.Input.URI) {
			return NewError(ErrInvalidTaskSchema, "local input must be an absolute path", false)
		}
	case InputURL:
		u, err := url.Parse(r.Input.URI)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return NewError(ErrInvalidTaskSchema, "URL input must use http or https", false)
		}
	default:
		return NewError(ErrInvalidTaskSchema, "input type must be local or url", false)
	}
	if !filepath.IsAbs(r.Output.Root) {
		return NewError(ErrInvalidTaskSchema, "output root must be an absolute path", false)
	}
	if !r.Steps.Audio.Enabled && !r.Steps.Video.Enabled && !r.Steps.Subtitles.Enabled && !r.Steps.Sprites.Enabled {
		return NewError(ErrInvalidTaskSchema, "at least one processing step must be enabled", false)
	}
	if r.Steps.Audio.Enabled {
		allowed := []string{"one_per_language", "all_languages", "default_only", "selected_languages"}
		if !slices.Contains(allowed, r.Steps.Audio.Strategy) {
			return NewError(ErrInvalidTaskSchema, "invalid audio strategy", false)
		}
		if r.Steps.Audio.Strategy == "selected_languages" && len(r.Steps.Audio.Languages) == 0 {
			return NewError(ErrInvalidTaskSchema, "selected_languages requires at least one language", false)
		}
	}
	if r.Steps.Video.Enabled {
		if len(r.Steps.Video.Profiles) == 0 {
			return NewError(ErrInvalidTaskSchema, "at least one video profile is required", false)
		}
		for _, profile := range r.Steps.Video.Profiles {
			if !slices.Contains([]string{"package", "auto", "720p", "1080p", "2160p"}, profile) {
				return NewError(ErrInvalidTaskSchema, fmt.Sprintf("invalid video profile %q", profile), false)
			}
		}
		if len(r.Steps.Video.Profiles) > 1 && slices.Contains(r.Steps.Video.Profiles, "auto") {
			return NewError(ErrInvalidTaskSchema, "auto cannot be combined with explicit profiles", false)
		}
	}
	if r.Steps.Sprites.Enabled {
		if r.Steps.Sprites.Columns <= 0 || r.Steps.Sprites.Rows <= 0 {
			return NewError(ErrInvalidTaskSchema, "sprite columns and rows must be positive", false)
		}
		if r.Steps.Sprites.Quality < 1 || r.Steps.Sprites.Quality > 100 || r.Steps.Sprites.Effort < 0 || r.Steps.Sprites.Effort > 9 {
			return NewError(ErrInvalidTaskSchema, "sprite quality or effort is outside the supported range", false)
		}
		if r.Steps.Sprites.FrameFormat != "" && !slices.Contains([]string{"png", "ppm"}, r.Steps.Sprites.FrameFormat) {
			return NewError(ErrInvalidTaskSchema, "sprite frame_format must be png or ppm", false)
		}
	}
	return nil
}
