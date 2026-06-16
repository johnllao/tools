//go:build windows

package main

import "os"

// fileInoImpl returns 0 on Windows, where inode-based rotation detection is
// not available. On Windows, only file truncation (size < currentSize) is
// detected.
func fileInoImpl(info os.FileInfo) uint64 {
	return 0
}
