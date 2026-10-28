//go:build linux

package main

import (
	"os"
	"syscall"
)

// diskUsage is what a file really takes on the disk: a film being streamed is a sparse file
// as long as the whole film, with only the pieces fetched so far written.
func diskUsage(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}

// freeSpace is the room left on the disk holding path, in bytes (a lot if it can't tell).
func freeSpace(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 1 << 50
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
