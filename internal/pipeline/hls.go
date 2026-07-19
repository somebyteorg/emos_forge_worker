package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func setHLSDefaultAudio(masterPath, playlistPath string) error {
	data, err := os.ReadFile(masterPath)
	if err != nil {
		return fmt.Errorf("read HLS master playlist: %w", err)
	}
	target := `URI="` + filepath.ToSlash(playlistPath) + `"`
	lines := strings.Split(string(data), "\n")
	changed := false
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-MEDIA:") || !strings.Contains(line, "TYPE=AUDIO") {
			continue
		}
		if strings.Contains(line, target) {
			lines[i] = setHLSAttribute(line, "DEFAULT", "YES")
			lines[i] = setHLSAttribute(lines[i], "AUTOSELECT", "YES")
			changed = true
			continue
		}
		if strings.Contains(line, "DEFAULT=YES") {
			lines[i] = setHLSAttribute(line, "DEFAULT", "NO")
		}
	}
	if !changed {
		return nil
	}
	info, err := os.Stat(masterPath)
	if err != nil {
		return fmt.Errorf("stat HLS master playlist: %w", err)
	}
	if err := os.WriteFile(masterPath, []byte(strings.Join(lines, "\n")), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write HLS master playlist: %w", err)
	}
	return nil
}

func setHLSAttribute(line, key, value string) string {
	prefix := key + "="
	parts := strings.Split(line, ",")
	for i, part := range parts {
		if strings.HasPrefix(part, prefix) {
			parts[i] = prefix + value
			return strings.Join(parts, ",")
		}
	}
	return line + "," + prefix + value
}
