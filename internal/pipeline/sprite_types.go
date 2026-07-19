package pipeline

const spriteStartAfterSeconds = 3.0

type spriteSize struct {
	Name   string
	Width  int
	Height int
}

type spriteMetadata struct {
	MediaID               string    `json:"media_id,omitempty"`
	Path                  string    `json:"path"`
	Width                 int       `json:"width"`
	Height                int       `json:"height"`
	CellWidth             int       `json:"cell_width"`
	CellHeight            int       `json:"cell_height"`
	Columns               int       `json:"columns"`
	Rows                  int       `json:"rows"`
	GridRows              int       `json:"grid_rows,omitempty"`
	FrameStart            int       `json:"frame_start"`
	FrameCount            int       `json:"frame_count"`
	FirstTimestampSeconds float64   `json:"first_timestamp_seconds"`
	LastTimestampSeconds  float64   `json:"last_timestamp_seconds"`
	IntervalSeconds       float64   `json:"interval_seconds"`
	Mode                  string    `json:"mode,omitempty"`
	TimestampsSeconds     []float64 `json:"timestamps_seconds,omitempty"`
}

type selectedSpriteFrame struct {
	KeyframeOrdinal int
	SeekTimestamp   float64
	Timestamp       float64
}
