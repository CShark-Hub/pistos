package plugin

import (
	"context"
	"os"
	"strings"
	"testing"

	"example.com/pistos/internal/runner"
	"example.com/pistos/internal/testutil/fakeworker"
)

func TestMain(m *testing.M) {
	fakeworker.MaybeServe()
	os.Exit(m.Run())
}

func workerCmd(t *testing.T, mode string, extraEnv ...string) runner.Cmd {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return runner.Cmd{Path: exe, Env: append([]string{fakeworker.EnvMode + "=" + mode}, extraEnv...)}
}

func TestWorkerAnalyzeAndShutdown(t *testing.T) {
	ctx := context.Background()
	w, err := StartWorker(ctx, workerCmd(t, fakeworker.ModeProgress), "fake", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	res, err := w.Analyze(ctx, AnalyzeParams{Capability: "fake-lint", Project: "p", Tree: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "complete" || res.Tree != "t1" || len(res.Diagnostics) != 1 {
		t.Fatalf("progress notifications must not replace the final result: %+v", res)
	}
	if err := w.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRejectsUnsupportedProtocol(t *testing.T) {
	_, err := StartWorker(context.Background(), workerCmd(t, fakeworker.ModeBadProtocol), "fake", "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "protocol 99") {
		t.Fatalf("want protocol error, got %v", err)
	}
}

func TestWorkerFailures(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want string
	}{
		{fakeworker.ModeMalformed, "malformed worker frame"},
		{fakeworker.ModeHuge, ErrFrameTooLarge.Error()},
		{fakeworker.ModeCrash, ErrWorkerExited.Error()},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx := context.Background()
			w, err := StartWorker(ctx, workerCmd(t, tc.mode), "fake", "1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			defer w.Kill()
			_, err = w.Analyze(ctx, AnalyzeParams{Tree: "t"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
