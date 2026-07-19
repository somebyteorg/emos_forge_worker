package config

import (
	"os"
	"strings"
)

func Load(envFile string) (Config, error) {
	values := make(map[string]string)
	if err := loadOptionalFile(".env", values); err != nil {
		return Config{}, err
	}
	if envFile != "" {
		if err := loadRequiredFile(envFile, values); err != nil {
			return Config{}, err
		}
	}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok && (strings.HasPrefix(key, "FORGE_") || strings.HasPrefix(key, "EMOS_")) {
			values[key] = value
		}
	}
	cfg := Defaults()
	if err := apply(&cfg, values); err != nil {
		return Config{}, err
	}
	if err := cfg.normalizePaths(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
