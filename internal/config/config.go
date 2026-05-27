package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

type Config struct {
	OllamaURL     string          `json:"ollama_url"`
	Model         string          `json:"model"`
	Temperature   float64         `json:"temperature"`
	ContextLen    int             `json:"context_length"`
	MaxMessages   int             `json:"max_messages"`
	AutoApprove   map[string]bool `json:"auto_approve"`
	WorkDir       string          `json:"work_dir"`
}

func Default() *Config {
	return &Config{
		OllamaURL:   "http://localhost:11434",
		Temperature: 0.7,
		ContextLen:  8192,
		MaxMessages: 40,
		AutoApprove: map[string]bool{
			"read_file":   true,
			"list_dir":    true,
			"find_files":  true,
			"fetch_url":   true,
			"web_search":  true,
		},
	}
}

func configPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "cli-agent", "config.json"), nil
}

func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return Default(), nil
	}

	slog.Debug("loading config", "path", path)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("config not found, using defaults")
			return Default(), nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	slog.Debug("config loaded", "ollama_url", cfg.OllamaURL, "model", cfg.Model)
	return cfg, nil
}

func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}
