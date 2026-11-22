//go:build !linux

package main

// lanUnlockSupported: the gateway MAC check is Linux-only; elsewhere the
// home-network unlock stays off (the password is always needed).
const lanUnlockSupported = false

func readNetSnap() netSnap { return netSnap{} }
