//go:build windows

package main

// The crash keeper (a parent process that holds Claude's pipes so sessions
// survive a Quill crash) passes file descriptors over a Unix socket; on
// Windows Quill runs without it and resumes sessions with --resume instead.

const keeperEnv = "QUILL_KEEPER"

type keeperClient struct{}

func newKeeperClient() *keeperClient { return nil }

func runKeeper(args []string, initial *restoreState) int { return runWindow() }

func (d *desk) keepSession(s *deskSession) {}

func (d *desk) unkeepSession(id string) {}

func (d *desk) keepView() {}
