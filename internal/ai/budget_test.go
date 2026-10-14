package ai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"example.com/pistos/internal/config"
)

// flaky fails every attempt it is allowed to make, asking BeforeRetry like the real client does.
type flaky struct{ attempts int }

func (f *flaky) Complete(_ context.Context, r Request) (Response, error) {
	for {
		f.attempts++
		if f.attempts == maxAttempts || !r.BeforeRetry() {
			return Response{Attempts: f.attempts}, errors.New("HTTP 503: busy")
		}
	}
}

type fixed struct{ usage Usage }

func (f fixed) Complete(context.Context, Request) (Response, error) {
	return Response{Content: "{}", Usage: f.usage, Attempts: 1}, nil
}

func testEngine(t *testing.T, b config.AIBudget, p Provider) *engine {
	price := func(v float64) *float64 { return &v }
	cfg := &config.Config{
		AI:     config.AI{Budget: b},
		Models: map[string]config.ModelAlias{"m": {Backend: "openai-compatible", Model: "x", PromptUSDPerMTok: price(2), CompletionUSDPerMTok: price(10)}},
	}
	in := Input{Repo: t.TempDir(), Config: cfg, NewProvider: func(string, config.ModelAlias) (Provider, error) { return p, nil }}
	return &engine{in: in, cacheDir: t.TempDir(), meter: newMeter(b)}
}

var msgs = []Message{{Role: "system", Content: strings.Repeat("s", 299)}, {Role: "user", Content: "u"}}

func TestEstimateIsConservative(t *testing.T) {
	e := testEngine(t, config.AIBudget{}, nil)
	c := estimate(e.in.Config.Models["m"], msgs, 50)
	// ceil(300 / 3) prompt tokens plus the whole completion allowance.
	if c.tokens != 150 || !c.known || c.usd != 100*2/1e6+50*10/1e6 {
		t.Fatalf("estimate: %+v", c)
	}
}

func TestRetriesReserveEachAttemptAndFailuresStayCharged(t *testing.T) {
	// max_tokens 50 → 150 tokens per attempt; the cap has room for two attempts, not three.
	p := &flaky{}
	e := testEngine(t, config.AIBudget{MaxTokens: 50, MaxReviewTokens: 350}, p)
	_, err := e.call(context.Background(), "discovery/x", "m", msgs, "k", false)
	if err == nil || !strings.Contains(err.Error(), "not retried: skipped: AI budget") || errors.Is(err, ErrBudget) {
		t.Fatalf("a retry the cap cannot cover is not made, and the call failed rather than skipped: %v", err)
	}
	sp := e.meter.report()
	if p.attempts != 2 || sp.ChargedTokens != 300 || e.meter.reserved.tokens != 0 || sp.Calls != 1 {
		t.Fatalf("both failed attempts keep their reservation: attempts %d %+v reserved %d", p.attempts, sp, e.meter.reserved.tokens)
	}
	// The cap is now exhausted for anything that needs a provider.
	if _, err := e.call(context.Background(), "discovery/y", "m", msgs, "k2", false); !errors.Is(err, ErrBudget) {
		t.Fatalf("expected a budget skip: %v", err)
	}
	if sp := e.meter.report(); strings.Join(sp.Skipped, ",") != "discovery/y" {
		t.Fatalf("skipped: %+v", sp)
	}
}

func TestSettleReplacesReservationWithUsage(t *testing.T) {
	e := testEngine(t, config.AIBudget{MaxTokens: 50, MaxReviewUSD: 1}, fixed{Usage{PromptTokens: 1000, CompletionTokens: 100}})
	if _, err := e.call(context.Background(), "triage", "m", msgs, "k", false); err != nil {
		t.Fatal(err)
	}
	sp := e.meter.report()
	if sp.ChargedTokens != 1100 || sp.ChargedUSD != 1000*2/1e6+100*10/1e6 || !sp.USDKnown || e.meter.reserved.tokens != 0 {
		t.Fatalf("usage replaces the reservation: %+v", sp)
	}
	// A recorded call replays for free, even with a cap that leaves no room.
	e2 := testEngine(t, config.AIBudget{MaxTokens: 50, MaxReviewTokens: 1}, nil)
	e2.cacheDir = e.cacheDir
	if _, err := e2.call(context.Background(), "triage", "m", msgs, "k", false); err != nil {
		t.Fatalf("replay must not be capped: %v", err)
	}
	if sp := e2.meter.report(); sp.Replayed != 1 || sp.ChargedTokens != 0 {
		t.Fatalf("replay spend: %+v", sp)
	}
}

func TestSuccessWithoutUsageKeepsReservation(t *testing.T) {
	e := testEngine(t, config.AIBudget{MaxTokens: 50, MaxReviewTokens: 1000}, fixed{})
	if _, err := e.call(context.Background(), "triage", "m", msgs, "k", false); err != nil {
		t.Fatal(err)
	}
	if sp := e.meter.report(); sp.ChargedTokens != 150 {
		t.Fatalf("unreported usage is charged at the reservation: %+v", sp)
	}
}
