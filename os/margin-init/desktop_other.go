//go:build !linux

package main

import (
	"os"
	"syscall"
)

func mapFile(path string) ([]byte, func(), error) {
	b, err := os.ReadFile(path)
	return b, func() {}, err
}

func alpineAttr() *syscall.SysProcAttr { return nil }
