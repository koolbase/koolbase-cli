package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const configFileName = "config.json"

type Config struct {
	APIKey         string `json:"api_key"`
	Email          string `json:"email,omitempty"`
	BaseURL        string `json:"base_url"`
	ProjectID      string `json:"project_id"`
	FlutterSDKPath string `json:"flutter_sdk_path,omitempty"`
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".koolbase"), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

func Load() (*Config, error) {
	cfg := &Config{}

	// Read the login file if present. Its absence is not an error: CI and
	// other headless contexts configure entirely through the environment.
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	if data, rerr := os.ReadFile(path); rerr == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	} else if !errors.Is(rerr, os.ErrNotExist) {
		return nil, rerr
	}

	// Environment overrides the file, so pipelines can inject secrets.
	if v := os.Getenv("KOOLBASE_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("KOOLBASE_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("KOOLBASE_PROJECT_ID"); v != "" {
		cfg.ProjectID = v
	}

	if v := os.Getenv("KOOLBASE_FLUTTER_SDK"); v != "" {
		cfg.FlutterSDKPath = v
	}

	if cfg.APIKey == "" || cfg.BaseURL == "" {
		return nil, errors.New("not configured — run: koolbase login, or set KOOLBASE_API_KEY and KOOLBASE_BASE_URL")
	}
	return cfg, nil
}

func Save(cfg *Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	path, err := configPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

func Clear() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}
