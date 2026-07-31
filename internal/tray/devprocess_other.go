//go:build !windows

package tray

import "errors"

// NewProcessRunner on non-Windows refuses to spawn: the Development section
// is a Windows desktop affordance (the tray UI itself only exists there).
func NewProcessRunner() ProcessRunner { return unsupportedRunner{} }

type unsupportedRunner struct{}

func (unsupportedRunner) Start(ProcessSpec) (ProcessHandle, error) {
	return nil, errors.New("tray: dev process supervision is unsupported on this platform")
}
