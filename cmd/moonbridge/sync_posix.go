//go:build !windows

package main

import "os"

// syncDir fsyncs a directory handle to ensure directory metadata is persisted.
// Supported on Unix-like systems; a no-op on Windows.
func syncDir(dir *os.File) error {
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}
