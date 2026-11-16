//go:build !windows

package platform

import (
	"os"
	"path/filepath"
)

// New returns the simulated platform: nothing outside the sandbox folder is
// touched and no real software is installed.
func New() Platform {
	dir := os.Getenv("DEVNEST_SIM_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "devnest-sim")
	}
	return NewSim(dir)
}
