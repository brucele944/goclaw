//go:build windows

package tools

import (
	"testing"
	"time"
)

// The pid-accounting helpers live in shell_abort_test.go, which is POSIX-only
// (the harness writes a pid file). Without Windows definitions the whole
// internal/tools test package failed to build here, so *no* test in this package
// — including the Windows-specific delegation-artifact publication code — could
// run on Windows. These stubs skip the dependent tests instead.

func waitForRecordedPIDs(t *testing.T, pidFile string, want int, timeout time.Duration) []string {
	t.Helper()
	t.Skipf("pid-file process accounting is not available on Windows (pid file %q, want %d pids, timeout %s)",
		pidFile, want, timeout)
	return nil
}

func findLivePIDs(t *testing.T, pids []string) []string {
	t.Helper()
	t.Skip("process liveness probes are not implemented on Windows")
	return nil
}
