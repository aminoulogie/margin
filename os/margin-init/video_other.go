//go:build !linux

package main

import (
	"context"
	"os"
	"os/exec"
)

func alpineCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

func setPipeSize(*os.File, int) {}
