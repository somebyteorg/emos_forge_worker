package doctor

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func (r Report) Human() string {
	var builder strings.Builder
	passed, warnings, failed := r.counts()
	builder.WriteString("forge-worker doctor\n")
	fmt.Fprintf(&builder, "checked_at: %s\n", r.CheckedAt)
	fmt.Fprintf(&builder, "summary: %d passed, %d warning, %d failed\n", passed, warnings, failed)
	appendEnvSummary(&builder, r.Env)

	for _, section := range []string{"Configuration", "Tools", "Storage"} {
		checks := r.sectionChecks(section)
		if len(checks) == 0 {
			continue
		}
		builder.WriteByte('\n')
		builder.WriteString(section)
		builder.WriteByte('\n')
		for _, check := range checks {
			appendCheckLine(&builder, check)
		}
	}

	builder.WriteByte('\n')
	if r.OK {
		builder.WriteString("doctor: all required checks passed\n")
	} else {
		builder.WriteString("doctor: one or more required checks failed\n")
	}
	return builder.String()
}

func appendEnvSummary(builder *strings.Builder, env EnvSummary) {
	builder.WriteByte('\n')
	builder.WriteString("Environment\n")
	fmt.Fprintf(builder, "  %-26s %s\n", "EMOS_URL", printable(env.EMOSURL))
	fmt.Fprintf(builder, "  %-26s %t\n", "EMOS_TOKEN set", env.EMOSTokenSet)
	fmt.Fprintf(builder, "  %-26s %s\n", "EMOS_FORGE_WORKER_ID", printable(env.EMOSForgeWorkerID))
	fmt.Fprintf(builder, "  %-26s %s\n", "FORGE_OUTPUT_DIR", printable(env.OutputDir))
	fmt.Fprintf(builder, "  %-26s %d\n", "FORGE_CPU_LIMIT", env.CPULimit)
	fmt.Fprintf(builder, "  %-26s %s\n", "FORGE_HTTP_TIMEOUT", printable(env.HTTPTimeout))
	fmt.Fprintf(builder, "  %-26s %s\n", "FORGE_HEARTBEAT_INTERVAL", printable(env.HeartbeatInterval))
	fmt.Fprintf(builder, "  %-26s %d\n", "EMOS_UPLOAD_CONCURRENCY", env.UploadConcurrency)
	fmt.Fprintf(builder, "  %-26s %d\n", "EMOS_UPLOAD_RETRY_MAX", env.UploadRetryMax)
	fmt.Fprintf(builder, "  %-26s %t\n", "EMOS_UPLOAD_DELETE_ARTIFACTS", env.UploadDeleteArtifacts)
	fmt.Fprintf(builder, "  %-26s %s\n", "FORGE_FFMPEG_PATH", printable(env.FFmpegPath))
	fmt.Fprintf(builder, "  %-26s %s\n", "FORGE_FFPROBE_PATH", printable(env.FFprobePath))
	fmt.Fprintf(builder, "  %-26s %s\n", "FORGE_PACKAGER_PATH", printable(env.PackagerPath))
	fmt.Fprintf(builder, "  %-26s %s\n", "FORGE_VIPS_PATH", printable(env.VIPSPath))
}

func printable(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return value
}

func (r Report) counts() (passed, warnings, failed int) {
	for _, check := range r.Checks {
		switch check.Status {
		case Pass:
			passed++
		case Warn:
			warnings++
		case Fail:
			failed++
		}
	}
	return passed, warnings, failed
}

func (r Report) sectionChecks(section string) []Check {
	var checks []Check
	for _, check := range r.Checks {
		if checkSection(check.Name) == section {
			checks = append(checks, check)
		}
	}
	return checks
}
