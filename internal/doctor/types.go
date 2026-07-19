package doctor

import (
	"context"

	"forge_worker/internal/runner"
)

type Status string

const (
	Pass Status = "pass"
	Warn Status = "warning"
	Fail Status = "fail"
)

type Check struct {
	Name    string   `json:"name"`
	Status  Status   `json:"status"`
	Message string   `json:"message"`
	Command []string `json:"command,omitempty"`
}

type EnvSummary struct {
	EMOSURL               string `json:"emos_url"`
	EMOSTokenSet          bool   `json:"emos_token_set"`
	EMOSForgeWorkerID     string `json:"emos_forge_worker_id,omitempty"`
	OutputDir             string `json:"output_dir"`
	CPULimit              int    `json:"cpu_limit"`
	HTTPTimeout           string `json:"http_timeout"`
	HeartbeatInterval     string `json:"heartbeat_interval"`
	UploadConcurrency     int    `json:"upload_concurrency"`
	UploadRetryMax        int    `json:"upload_retry_max"`
	UploadDeleteArtifacts bool   `json:"upload_delete_artifacts"`
	FFmpegPath            string `json:"ffmpeg_path"`
	FFprobePath           string `json:"ffprobe_path"`
	PackagerPath          string `json:"packager_path"`
	VIPSPath              string `json:"vips_path"`
}

type Report struct {
	OK        bool       `json:"ok"`
	CheckedAt string     `json:"checked_at"`
	Env       EnvSummary `json:"env"`
	Checks    []Check    `json:"checks"`
}

type CommandRunner interface {
	Run(context.Context, runner.Spec) (runner.Result, error)
}

type Options struct {
	Debug bool
}

func (r *Report) add(check Check) {
	r.Checks = append(r.Checks, check)
	if check.Status == Fail {
		r.OK = false
	}
}
