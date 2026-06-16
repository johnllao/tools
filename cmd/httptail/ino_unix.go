//go:build !windows

package main

import (
	"os"
	"syscall"
)

// fileInoImpl extracts the inode number from a file's FileInfo on Unix
// platforms. The inode is used to detect log file rotation (create mode).
func fileInoImpl(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}
