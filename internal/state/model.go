package state

import (
	"time"

	"forge_worker/internal/task"
)

type TaskRecord struct {
	TaskUUID  string     `json:"task_uuid"`
	State     task.State `json:"state"`
	Progress  float64    `json:"progress_percent"`
	ProbeJSON string     `json:"probe_json,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type StepRecord struct {
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	State          string     `json:"state"`
	Progress       float64    `json:"progress_percent"`
	FPS            float64    `json:"fps,omitempty"`
	Speed          float64    `json:"speed,omitempty"`
	Attempt        int        `json:"attempt"`
	MaxAttempts    int        `json:"max_attempts"`
	CommandSummary string     `json:"command_summary,omitempty"`
	DetailsJSON    string     `json:"details_json,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type StepCommandRecord struct {
	StepName string `json:"step_name"`
	Summary  string `json:"summary"`
}

type StepSpec struct {
	Name         string
	Kind         string
	Weight       float64
	MaxAttempts  int
	Dependencies []string
}

type ArtifactRecord struct {
	StepName     string `json:"step_name"`
	Kind         string `json:"kind"`
	RelativePath string `json:"relative_path"`
	SizeBytes    int64  `json:"size_bytes"`
	Committed    bool   `json:"committed"`
	MetadataJSON string `json:"metadata_json,omitempty"`
}

type ArtifactSpec struct {
	StepName     string
	Kind         string
	RelativePath string
	SizeBytes    int64
	Committed    bool
	Metadata     any
}

type WarningRecord struct {
	StepName    string `json:"step_name"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	DetailsJSON string `json:"details_json,omitempty"`
}

type WarningSpec struct {
	StepName string
	Code     string
	Message  string
	Details  any
}

type TaskCheckpoint struct {
	Request   task.Request        `json:"request"`
	Task      TaskRecord          `json:"task"`
	Steps     []StepRecord        `json:"steps"`
	Artifacts []ArtifactRecord    `json:"artifacts"`
	Warnings  []WarningRecord     `json:"warnings"`
	Commands  []StepCommandRecord `json:"commands"`
}
