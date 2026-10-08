package cli

import (
	"os"
	"testing"
)

// TestMain shares acquired tools across tests so each pinned tool is downloaded once per run.
// Set PISTOS_TEST_TOOLS_DIR to keep them between runs.
func TestMain(m *testing.M) {
	dir := os.Getenv("PISTOS_TEST_TOOLS_DIR")
	cleanup := false
	if dir == "" {
		var err error
		if dir, err = os.MkdirTemp("", "pistos-test-tools-"); err != nil {
			panic(err)
		}
		cleanup = true
	}
	os.Setenv("PISTOS_TOOLS_DIR", dir)
	os.Setenv("DOTNET_CLI_TELEMETRY_OPTOUT", "1")
	os.Setenv("DOTNET_NOLOGO", "1")
	code := m.Run()
	if cleanup {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}
