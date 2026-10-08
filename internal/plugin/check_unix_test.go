//go:build !windows

package plugin

import (
	"context"
	"testing"
	"time"

	"example.com/pistos/internal/finding"
)

func shellCheck(t *testing.T, script string) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(`{"schema":1,"name":"sh","version":"1","languages":["sh"],
	  "tools":{"sh":{"command":"sh","lookup":["path"],"version_args":[],"version_regex":"(.*)"}},
	  "checks":{"c":{"tool":"sh","args":["-c",` + "\"" + script + "\"" + `],"format":"go-vet","scope":"file","weight":1,"exit_codes":{"0":"clean"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTimeoutAndTruncationAreIncomplete(t *testing.T) {
	for name, tc := range map[string]struct {
		script  string
		timeout time.Duration
	}{
		"timeout":   {"sleep 30", 200 * time.Millisecond},
		"truncated": {"head -c 70000000 /dev/zero >&2", 0},
	} {
		_, status, err := Execute(context.Background(), Invocation{
			Manifest: shellCheck(t, tc.script), CheckID: "c", Tool: ResolvedTool{Name: "sh", Path: "/bin/sh"},
			ProjectDir: t.TempDir(), ScratchDir: t.TempDir(), Timeout: tc.timeout,
		})
		if status != finding.StatusIncomplete || err == nil {
			t.Errorf("%s: status %s (%v); a %s result must never be clean", name, status, err, name)
		}
	}
}
