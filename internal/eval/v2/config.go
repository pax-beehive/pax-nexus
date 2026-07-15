package v2

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func LoadConfig(path string) (Config, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read eval config: %w", err)
	}
	var config Config
	if err := yaml.Unmarshal(input, &config); err != nil {
		return Config{}, fmt.Errorf("decode eval config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}
