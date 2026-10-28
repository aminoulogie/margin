//go:build linux

package main

import (
	"os"
	"syscall"
)

// mapFile maps a file read-only: Xvfb's screen, seen as it draws.
func mapFile(path string) ([]byte, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	b, err := syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, err
	}
	return b, func() { syscall.Munmap(b) }, nil
}

func alpineAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Chroot: alpineRoot, Setpgid: true}
}
