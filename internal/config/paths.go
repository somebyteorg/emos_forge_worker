package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func (c *Config) normalizePaths() error {
	var err error
	if c.OutputDir, err = filepath.Abs(c.OutputDir); err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	return nil
}

func (c Config) EnsureRuntimeDirs() error {
	if err := ensureDirectory(c.OutputDir, "output directory"); err != nil {
		return err
	}
	return nil
}

func ensureDirectory(path, label string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", label, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", label, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory: %s", label, path)
	}
	return nil
}
