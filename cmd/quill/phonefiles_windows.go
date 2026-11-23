package main

import "os"

// openNoFollow: Windows has no O_NOFOLLOW; the caller's os.SameFile check
// against the Lstat of the resolved path catches a swap.
func openNoFollow(p string) (*os.File, error) {
	return os.Open(p)
}
