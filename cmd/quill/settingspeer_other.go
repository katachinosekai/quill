//go:build !linux

package main

import "net"

// Elsewhere the session token alone says who asks (docs/design/quill-operator.md §3.8).

const peerCredsSupported = false

func peerChain(net.Conn) []int { return nil }
