//go:build windows

package cli

import "testing"

func waitForFile(t *testing.T, p string)         { t.Skip("unix-only helper") }
func assertPidGone(t *testing.T, pidFile string) { t.Skip("unix-only helper") }
