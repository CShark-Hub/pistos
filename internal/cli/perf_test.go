package cli

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Budgets derive from the measured slice in docs/benchmarks/results.md (go-small: no-op 18 ms,
// warm function edit 113 ms) with roughly 15x headroom for shared CI runners. A failure means a
// real regression, not noise.
const (
	budgetNoOp     = 300 * time.Millisecond
	budgetWarmEdit = 2 * time.Second
)

func p95(ds []time.Duration) time.Duration {
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[int(float64(len(ds)-1)*0.95)]
}

func TestPerformanceBudget(t *testing.T) {
	if os.Getenv("PISTOS_PERF_BUDGET") != "1" {
		t.Skip("set PISTOS_PERF_BUDGET=1 to enforce latency budgets")
	}
	r := setupGo(t, "go-clean", configFor(strings.Replace(goProject, `checks = ["go-vet"]`, `checks = ["go-vet", "metrics"]`, 1)))
	runReview(t, r.Dir)
	var noop, hook, warm []time.Duration
	for i := 0; i < 10; i++ {
		start := time.Now()
		run(t, r.Dir, "review")
		noop = append(noop, time.Since(start))
	}
	// A pre-commit hook with nothing staged is the most frequent hook run.
	for i := 0; i < 10; i++ {
		start := time.Now()
		run(t, r.Dir, "hooks", "run", "pre-commit")
		hook = append(hook, time.Since(start))
	}
	for i := 0; i < 5; i++ {
		r.Write("main.go", r.Read("main.go")+"\nfunc edit"+strings.Repeat("x", i+1)+"() {}\n")
		start := time.Now()
		run(t, r.Dir, "review")
		warm = append(warm, time.Since(start))
	}
	if got := p95(noop); got > budgetNoOp {
		t.Errorf("no-op review p95 %s exceeds budget %s", got, budgetNoOp)
	}
	if got := p95(hook); got > budgetNoOp {
		t.Errorf("no-op pre-commit hook p95 %s exceeds budget %s", got, budgetNoOp)
	}
	if got := p95(warm); got > budgetWarmEdit {
		t.Errorf("warm function edit p95 %s exceeds budget %s", got, budgetWarmEdit)
	}
	t.Logf("no-op p95 %s, no-op pre-commit hook p95 %s, warm edit p95 %s", p95(noop), p95(hook), p95(warm))
}
