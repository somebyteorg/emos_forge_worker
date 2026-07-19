package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsUseCurrentWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	cfg := Defaults()
	if cfg.OutputDir != filepath.Join(dir, "output") {
		t.Fatalf("OutputDir = %s", cfg.OutputDir)
	}
	if cfg.EncryptionMode != "clearkey" {
		t.Fatalf("EncryptionMode = %s", cfg.EncryptionMode)
	}
	if cfg.SpriteFrameFormat != "png" {
		t.Fatalf("SpriteFrameFormat = %s", cfg.SpriteFrameFormat)
	}
	if !cfg.UploadTitledSubtitlesOnly {
		t.Fatal("UploadTitledSubtitlesOnly should default to true")
	}
}

func TestLoadAllowsUploadingUntitledSubtitles(t *testing.T) {
	t.Setenv("EMOS_UPLOAD_TITLED_SUBTITLES_ONLY", "false")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UploadTitledSubtitlesOnly {
		t.Fatal("UploadTitledSubtitlesOnly should be false")
	}
}

func TestLoadNormalizesRelativeRuntimePaths(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	t.Setenv("FORGE_OUTPUT_DIR", "custom-output")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OutputDir != filepath.Join(dir, "custom-output") {
		t.Fatalf("OutputDir = %s", cfg.OutputDir)
	}
}

func TestLoadEncryptionMode(t *testing.T) {
	t.Setenv("FORGE_ENCRYPTION_MODE", "ClearKey")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EncryptionMode != "clearkey" {
		t.Fatalf("EncryptionMode = %s", cfg.EncryptionMode)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadSpriteFrameFormat(t *testing.T) {
	t.Setenv("FORGE_SPRITE_FRAME_FORMAT", "PPM")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SpriteFrameFormat != "ppm" {
		t.Fatalf("SpriteFrameFormat = %s", cfg.SpriteFrameFormat)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsUnsupportedSpriteFrameFormat(t *testing.T) {
	cfg := Defaults()
	cfg.SpriteFrameFormat = "jpg"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected unsupported sprite frame format to fail")
	}
}

func TestValidateRejectsUnsupportedEncryptionMode(t *testing.T) {
	cfg := Defaults()
	cfg.EncryptionMode = "widevine"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected unsupported encryption mode to fail")
	}
}

func TestValidateRejectsInvalidRuntimeLimits(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{name: "retry attempts", change: func(cfg *Config) { cfg.StepRetryMax = 0 }},
		{name: "retry range", change: func(cfg *Config) { cfg.RetryMax = cfg.RetryInitial / 2 }},
		{name: "sprite grid", change: func(cfg *Config) { cfg.SpriteColumns = 0 }},
		{name: "upload concurrency", change: func(cfg *Config) { cfg.UploadConcurrency = 0 }},
		{name: "upload chunk", change: func(cfg *Config) { cfg.UploadChunkSizeBytes = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Defaults()
			test.change(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected invalid %s to fail", test.name)
			}
		})
	}
}

func TestEnsureRuntimeDirsCreatesMissingDirectories(t *testing.T) {
	dir := t.TempDir()
	cfg := Defaults()
	cfg.OutputDir = filepath.Join(dir, "output")
	if err := cfg.EnsureRuntimeDirs(); err != nil {
		t.Fatalf("EnsureRuntimeDirs: %v", err)
	}
	for _, path := range []string{cfg.OutputDir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", path)
		}
	}
}

func TestEnsureRuntimeDirsRejectsFileInPlaceOfOutputDirectory(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "output")
	if err := os.WriteFile(output, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := Defaults()
	cfg.OutputDir = output
	if err := cfg.EnsureRuntimeDirs(); err == nil {
		t.Fatalf("expected output file to be rejected")
	}
}

func withWorkingDirectory(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}
