//go:build !windows

package main

import (
	"os"
	"syscall"
)

// openNoFollow opens a resolved path read-only without following a symlink
// put there after the check, and without blocking on a FIFO.
func openNoFollow(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
