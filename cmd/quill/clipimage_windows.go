//go:build windows

package main

import "errors"

// runClipTool: no image clipboard on Windows yet (copyImageTo says so before
// getting here; "Copy path" still works).
func runClipTool(argv []string, data []byte) error {
	return errors.New("not available on Windows yet")
}
