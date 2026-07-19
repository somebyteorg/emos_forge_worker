package doctor

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func firstOutputLine(outputs ...string) string {
	for _, output := range outputs {
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				return line
			}
		}
	}
	return ""
}

func matchingOutputLine(output, needle string) string {
	needle = strings.ToLower(needle)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.Contains(strings.ToLower(line), needle) {
			return line
		}
	}
	return ""
}

func avifEncoderLine(output string) string {
	inAVIFSection := false
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasSuffix(lower, "encoders:") {
			inAVIFSection = lower == "avif encoders:"
			continue
		}
		if inAVIFSection && strings.HasPrefix(line, "- ") {
			return line
		}
	}
	return ""
}

func debugCommand(path string, args []string, opt Options) []string {
	if !opt.Debug {
		return nil
	}
	command := make([]string, 0, len(args)+1)
	command = append(command, path)
	command = append(command, args...)
	return command
}

func concise(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	lines = slices.DeleteFunc(lines, func(line string) bool { return strings.TrimSpace(line) == "" })
	if len(lines) > 0 {
		return lines[len(lines)-1]
	}
	return err.Error()
}

func formatCommand(command []string) string {
	parts := make([]string, 0, len(command))
	for _, part := range command {
		if part == "" || strings.ContainsAny(part, " \t\n\"'\\") {
			parts = append(parts, strconv.Quote(part))
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

func statusLabel(status Status) string {
	switch status {
	case Pass:
		return "PASS"
	case Warn:
		return "WARN"
	case Fail:
		return "FAIL"
	default:
		return strings.ToUpper(string(status))
	}
}

func checkSection(name string) string {
	switch {
	case strings.HasPrefix(name, "config"):
		return "Configuration"
	case name == "output":
		return "Storage"
	default:
		return "Tools"
	}
}

func appendCheckLine(builder *strings.Builder, check Check) {
	fmt.Fprintf(builder, "  %-4s %-22s %s\n", statusLabel(check.Status), check.Name, check.Message)
	if len(check.Command) > 0 {
		fmt.Fprintf(builder, "       command: %s\n", formatCommand(check.Command))
	}
}
