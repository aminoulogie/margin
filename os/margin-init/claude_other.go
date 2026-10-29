//go:build !linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// On the PC (tests, previews) the Claude tab has no raw terminal; see claude_linux.go.

func ttyRaw(uintptr) (func(), error) { return func() {}, errors.New("no raw terminal here") }

func ttyCols(uintptr) int { return 80 }

func ownGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}

func claudeHome() string {
	if h := os.Getenv("MARGIN_CLAUDE_HOME"); h != "" {
		return h
	}
	return filepath.Join(os.TempDir(), "margin-claude")
}
