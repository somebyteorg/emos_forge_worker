package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

func loadOptionalFile(path string, dst map[string]string) error {
	values, err := godotenv.Read(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read env file %s: %w", path, err)
	}
	mergeEnvValues(dst, values)
	return nil
}

func loadRequiredFile(path string, dst map[string]string) error {
	values, err := godotenv.Read(path)
	if err != nil {
		return fmt.Errorf("read env file %s: %w", path, err)
	}
	mergeEnvValues(dst, values)
	return nil
}

func mergeEnvValues(dst, values map[string]string) {
	for key, value := range values {
		dst[key] = value
	}
}
