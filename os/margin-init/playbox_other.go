//go:build !linux

package main

import "os"

func diskUsage(fi os.FileInfo) int64 { return fi.Size() }

func freeSpace(string) int64 { return 1 << 50 }
