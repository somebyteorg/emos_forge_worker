package emos

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupUploadedArtifactsArchivesRootJSONAndRemovesTaskDirectory(t *testing.T) {
	outputDir := t.TempDir()
	root := filepath.Join(outputDir, "job-1")
	if err := os.MkdirAll(filepath.Join(root, "video", "1080p"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"manifest.json":             `{"schema_version":1}`,
		"log.json":                  `{"task":"job-1"}`,
		"upload_state.json":         `{"media":{}}`,
		"video/1080p/init.mp4":      "init",
		"video/1080p/00001.m4s":     "segment",
		"video/1080p/index.m3u8":    "playlist",
		"video/1080p/master.m3u8":   "master",
		"video/1080p/key-info.json": "key",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	if err := cleanupUploadedArtifacts(root); err != nil {
		t.Fatalf("cleanupUploadedArtifacts: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("task directory should be removed, stat err = %v", err)
	}
	for _, name := range []string{"manifest.json", "log.json", "upload_state.json"} {
		data, err := os.ReadFile(filepath.Join(outputDir, "_logs", "job-1", name))
		if err != nil {
			t.Fatalf("archived %s missing: %v", name, err)
		}
		if string(data) != files[name] {
			t.Fatalf("archived %s = %q, want %q", name, string(data), files[name])
		}
	}
	if _, err := os.Stat(filepath.Join(outputDir, "_logs", "job-1", "key-info.json")); !os.IsNotExist(err) {
		t.Fatalf("nested media json should not be archived at root, stat err = %v", err)
	}
}
