//go:build !windows

package main

import (
	"errors"
	"os/exec"
)

// Non-Windows stubs so the platform-independent parts build and test anywhere.

func isAdmin() bool { return false }

func relaunchElevated([]string) error { return errors.New("not supported on this platform") }

func hiddenCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

func setTitle(string) {}
