//go:build !windows

package svchost

import "errors"

// Run only works on Windows.
func Run(configPath string) error {
	return errors.New("service host mode is only available on Windows")
}
