package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"forge_worker/internal/runner"
)

func executable(report *Report, name, configured string) (string, bool) {
	path, err := exec.LookPath(configured)
	if err != nil {
		report.add(Check{Name: name, Status: Fail, Message: fmt.Sprintf("%s is not executable: %v", configured, err)})
		return "", false
	}
	report.add(Check{Name: name, Status: Pass, Message: path})
	return path, true
}

func checkFFmpeg(ctx context.Context, commandRunner CommandRunner, report *Report, path string, opt Options) {
	checkVersion(ctx, commandRunner, report, "ffmpeg.version", path, []string{"-version"}, opt)
	checkCommand(ctx, commandRunner, report, "ffmpeg.encoders", path, []string{"-hide_banner", "-encoders"}, []string{"libx264", "libx265", "aac"}, opt)
	checkCommand(ctx, commandRunner, report, "ffmpeg.decoders", path, []string{"-hide_banner", "-decoders"}, []string{"h264", "hevc", "aac"}, opt)
	checkCommand(ctx, commandRunner, report, "ffmpeg.filters", path, []string{"-hide_banner", "-filters"}, []string{"scale", "zscale", "tonemap", "format", "subtitles"}, opt)
}

func checkPackager(ctx context.Context, commandRunner CommandRunner, report *Report, path string, opt Options) {
	checkVersion(ctx, commandRunner, report, "packager.version", path, []string{"--version"}, opt)
}

func checkVIPS(ctx context.Context, commandRunner CommandRunner, report *Report, path string, opt Options) {
	checkVersion(ctx, commandRunner, report, "vips.version", path, []string{"--version"}, opt)
	args := []string{"-l"}
	result, err := commandRunner.Run(ctx, runner.Spec{Name: path, Args: args})
	command := debugCommand(path, args, opt)
	if err != nil {
		report.add(Check{Name: "vips.avif", Status: Fail, Message: concise(result.Stderr, err), Command: command})
		return
	}
	match := matchingOutputLine(result.Stdout+"\n"+result.Stderr, "heif")
	if match == "" {
		report.add(Check{Name: "vips.avif", Status: Fail, Message: "HEIF/AVIF support was not listed by vips -l", Command: command})
		return
	}
	report.add(Check{Name: "vips.avif", Status: Pass, Message: "HEIF/AVIF support listed: " + match, Command: command})
	checkHEIFEncAVIFEncode(ctx, commandRunner, report, opt)
}

func checkHEIFEncAVIFEncode(ctx context.Context, commandRunner CommandRunner, report *Report, opt Options) {
	name := "heif-enc"
	args := []string{"--list-encoders"}
	result, err := commandRunner.Run(ctx, runner.Spec{Name: name, Args: args})
	command := debugCommand(name, args, opt)
	if err != nil {
		report.add(Check{Name: "heif-enc.avif", Status: Fail, Message: concise(result.Stderr, err), Command: command})
		return
	}
	encoder := avifEncoderLine(result.Stdout + "\n" + result.Stderr)
	if encoder == "" {
		report.add(Check{Name: "heif-enc.avif", Status: Fail, Message: "AVIF encoder was not listed by heif-enc --list-encoders", Command: command})
		return
	}
	report.add(Check{Name: "heif-enc.avif", Status: Pass, Message: "AVIF encoder listed: " + encoder, Command: command})
}

func checkVersion(ctx context.Context, commandRunner CommandRunner, report *Report, name, path string, args []string, opt Options) {
	result, err := commandRunner.Run(ctx, runner.Spec{Name: path, Args: args})
	command := debugCommand(path, args, opt)
	if err != nil {
		report.add(Check{Name: name, Status: Fail, Message: concise(result.Stderr, err), Command: command})
		return
	}
	version := firstOutputLine(result.Stdout, result.Stderr)
	if version == "" {
		version = "version command completed with no output"
	}
	report.add(Check{Name: name, Status: Pass, Message: version, Command: command})
}

func checkCommand(ctx context.Context, commandRunner CommandRunner, report *Report, name, path string, args, required []string, opt Options) {
	result, err := commandRunner.Run(ctx, runner.Spec{Name: path, Args: args})
	command := debugCommand(path, args, opt)
	if err != nil {
		report.add(Check{Name: name, Status: Fail, Message: concise(result.Stderr, err), Command: command})
		return
	}
	output := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	var missing []string
	for _, capability := range required {
		if !strings.Contains(output, strings.ToLower(capability)) {
			missing = append(missing, capability)
		}
	}
	if len(missing) > 0 {
		report.add(Check{Name: name, Status: Fail, Message: "missing capabilities: " + strings.Join(missing, ", "), Command: command})
		return
	}
	message := "command completed successfully"
	if len(required) > 0 {
		message = "capabilities present: " + strings.Join(required, ", ")
	}
	report.add(Check{Name: name, Status: Pass, Message: message, Command: command})
}
