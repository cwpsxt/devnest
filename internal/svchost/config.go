// Package svchost lets this program act as a Windows service that keeps
// another program running. It is how software without service support of its
// own (Redis, Nginx, Kafka ...) gets start-on-boot and start/stop buttons.
package svchost

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Name string            `json:"name"`
	Exe  string            `json:"exe"`
	Args []string          `json:"args"`
	Dir  string            `json:"dir"`
	Env  map[string]string `json:"env,omitempty"`
	// StopExe/StopArgs is an optional graceful stop command ("nginx -s quit").
	// Without it the program gets Ctrl+C, and is killed if it ignores that.
	StopExe        string   `json:"stopExe,omitempty"`
	StopArgs       []string `json:"stopArgs,omitempty"`
	StopTimeoutSec int      `json:"stopTimeoutSec,omitempty"`
	LogFile        string   `json:"logFile,omitempty"`
}

func ConfigPath(configDir, name string) string {
	return filepath.Join(configDir, "services", name+".json")
}

func Save(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}
