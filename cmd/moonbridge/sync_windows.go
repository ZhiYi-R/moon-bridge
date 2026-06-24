//go:build windows

package main

import "os"

// syncDir fsyncs a directory handle.
// Windows does not support fsync on directory handles, so this is a no-op.
func syncDir(dir *os.File) error {
	return dir.Close()
}
