//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// torrentIOEnv picks how the torrent library reads and writes films. Its default maps a whole
// file into memory, and on this 32-bit tablet an 880 MB film didn't fit in what was left of
// Margin's ~3 GB of address space after a while: "mapping file: cannot allocate memory", seen
// by the player as "Input/output error" (2026-11-08). "classic" uses plain reads and writes.
// The library reads the variable in its own init, before main, so margin-init sets it and
// starts itself again (same process, same pid) once, at the very start.
const torrentIOEnv = "TORRENT_STORAGE_DEFAULT_FILE_IO"

func reexecForTorrentIO() {
	if os.Getenv(torrentIOEnv) != "" {
		return
	}
	exe, err := os.Executable()
	if err != nil || strings.HasSuffix(exe, " (deleted)") {
		return
	}
	err = syscall.Exec(exe, os.Args, withTorrentIOEnv(os.Environ()))
	// Only returns if it failed: carry on as before (films then read the default way).
	fmt.Fprintln(os.Stderr, "margin-init: restarting for plain torrent file reads:", err)
}

// withTorrentIOEnv is env with plain file reads for the torrent library.
func withTorrentIOEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, torrentIOEnv+"=") {
			out = append(out, e)
		}
	}
	return append(out, torrentIOEnv+"=classic")
}
