//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// alpineCmd is a program run inside Alpine (chroot), without the proxy: the player's
// programs only read from 127.0.0.1. With no Alpine root (tests), it runs as it is.
func alpineCtxCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = "/"
	cmd.Env = []string{"HOME=/root", "LANG=C.UTF-8", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	if alpineRoot != "" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: alpineRoot}
	}
	return cmd
}

// setPipeSize makes a pipe's buffer n bytes (F_SETPIPE_SZ, Linux 2.6.35 and later).
func setPipeSize(f *os.File, n int) {
	const fSetPipeSz = 1031
	syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), fSetPipeSz, uintptr(n))
}
