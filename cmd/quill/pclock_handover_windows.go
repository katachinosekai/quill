//go:build windows

package main

// On Windows the "exec" starts a new process; the device-lock state isn't
// handed over there, so a reload locks every device (as a restart does).

func passUnlocks([]byte) (string, func(), bool) { return "", nil, false }

func takeUnlocks() []byte { return nil }
