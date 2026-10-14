package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/pistos/internal/config"
)

// fakeProvider serves /models (OpenRouter-shaped) and OpenRouter's /key; only "Bearer good" is valid.
func fakeProvider(t *testing.T, publicModels bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	authed := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer good" }
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		if !publicModels && !authed(r) {
			http.Error(w, `{"error":"bad key"}`, http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[
			{"id":"acme/gpt-mini","name":"GPT mini","context_length":128000,"pricing":{"prompt":"0.00000015","completion":"0.0000006"},"supported_parameters":["response_format"]},
			{"id":"acme/gpt-large","name":"GPT large","context_length":200000,"pricing":{"prompt":"0.000002","completion":"0.000008"},"supported_parameters":["response_format"]},
			{"id":"other/llama-3","name":"Llama 3","supported_parameters":["structured_outputs"]},
			{"id":"other/no-json","name":"No JSON","supported_parameters":["temperature"]}]}`))
	})
	mux.HandleFunc("GET /v1/key", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":{"label":"test"}}`))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := config.RenderConfig([]string{config.Go}, []config.Project{{ID: "go", Root: ".", Ecosystem: config.Go, Checks: []string{"go-vet"}}})
	src += "\n[ai.budget]\nmax_candidates = 7\n"
	if err := os.MkdirAll(filepath.Join(dir, ".pistos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pistos", "config.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PISTOS_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials.json"))
	return dir
}

func runInput(t *testing.T, dir, input string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), append([]string{"-C", dir}, args...), strings.NewReader(input), &out, &errb)
	return code, out.String(), errb.String()
}

func TestAISetupOpenAICompatible(t *testing.T) {
	s := fakeProvider(t, false)
	dir := setupRepo(t)
	t.Setenv("PISTOS_AI_KEY", "")
	host := strings.TrimPrefix(s.URL, "http://")
	input := strings.Join([]string{
		"1",                            // provider
		"ftp://nope",                   // rejected endpoint
		host + "/v1/chat/completions/", // pasted without scheme, with the full path
		"bad", "good",                  // first key refused
		"",         // variable name: default
		"",         // save the key: yes
		"",         // passes: 3
		"gpt", "2", // search, pick from the list
		"",              // pass 2 repeats pass 1
		"other/llama-3", // exact id
		"",              // triage with a model
		"",              // triage model: pass 1's
		"",              // write
	}, "\n") + "\n"
	code, out, errs := runInput(t, dir, input, "ai", "setup")
	if code != 0 {
		t.Fatalf("code=%d\n%s\n%s", code, out, errs)
	}
	for _, want := range []string{"must be an http(s) URL", "using http://" + host + "/v1", "rejected the key", "✓ key accepted", "lists 4 model(s)", "→ acme/gpt-mini", "$0.15 in / $0.60 out"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".pistos", "config.toml"))
	if strings.Contains(string(raw), "good") {
		t.Fatal("the key must never be written into config.toml")
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AI.Enabled || len(cfg.AI.Discovery) != 3 || cfg.AI.Triage.Mode != "model" || cfg.AI.Budget.MaxCandidates != 7 || len(cfg.Projects) != 1 {
		t.Fatalf("unexpected config: %+v", cfg.AI)
	}
	if m := cfg.Models[cfg.AI.Discovery[1].Model]; m.Model != "acme/gpt-mini" || m.APIKeyEnv != "PISTOS_AI_KEY" || m.Endpoint != "http://"+host+"/v1" {
		t.Fatalf("pass 2: %+v", m)
	}
	if cfg.Models[cfg.AI.Discovery[2].Model].Model != "other/llama-3" || cfg.AI.Triage.Model != cfg.AI.Discovery[0].Model || len(cfg.Models) != 2 {
		t.Fatalf("aliases: %+v %+v", cfg.AI, cfg.Models)
	}
	// Catalog prices are written for spend estimates and caps; unpublished prices are left out.
	if m := cfg.Models[cfg.AI.Discovery[1].Model]; !m.Priced() || *m.PromptUSDPerMTok != 0.15 || *m.CompletionUSDPerMTok != 0.6 {
		t.Fatalf("prices: %+v", m)
	}
	if m := cfg.Models[cfg.AI.Discovery[2].Model]; m.PromptUSDPerMTok != nil || m.CompletionUSDPerMTok != nil {
		t.Fatalf("unknown prices must be omitted: %+v", m)
	}
	if !strings.Contains(string(raw), "prompt_usd_per_mtok = 0.15") || !strings.Contains(out, "Estimated cost per review: unknown (the endpoint publishes no prices for other/llama-3)") {
		t.Fatalf("prices and estimate:\n%s\n%s", raw, out)
	}
	fi, err := os.Stat(os.Getenv("PISTOS_CREDENTIALS_FILE"))
	if err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("credentials file must exist and be private: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(os.Getenv("PISTOS_CREDENTIALS_FILE")); !strings.Contains(string(b), `"PISTOS_AI_KEY": "good"`) {
		t.Fatalf("saved key: %s", b)
	}

	// A second run offers the saved key and keeps everything when replacement is declined.
	if code, out, _ := runInput(t, dir, "n\n", "ai", "setup"); code != 0 || !strings.Contains(out, "kept the existing configuration") {
		t.Fatalf("decline replace: %d %s", code, out)
	}
}

func TestAISetupOpenRouter(t *testing.T) {
	s := fakeProvider(t, true)
	old := openRouterEndpoint
	openRouterEndpoint = s.URL + "/v1"
	t.Cleanup(func() { openRouterEndpoint = old })
	dir := setupRepo(t)
	t.Setenv("OPENROUTER_API_KEY", "")
	input := strings.Join([]string{
		"2",             // OpenRouter
		"wrong", "good", // the public model list proves nothing; /key does
		"", "n", // default variable name, do not save
		"2",            // two passes
		"no-json", "n", // filtered out: not offered, not forced
		"large", "llama", // two vendors
		"2", // evidence triage
		"y",
	}, "\n") + "\n"
	code, out, errs := runInput(t, dir, input, "ai", "setup")
	if code != 0 {
		t.Fatalf("code=%d\n%s\n%s", code, out, errs)
	}
	for _, want := range []string{"rejected the key", "3 of 4 OpenRouter models", `no listed model matches "no-json"`, "export OPENROUTER_API_KEY=<your key>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Triage.Mode != "evidence" || len(cfg.AI.Discovery) != 2 || cfg.Models[cfg.AI.Discovery[0].Model].Model != "acme/gpt-large" {
		t.Fatalf("unexpected config: %+v %+v", cfg.AI, cfg.Models)
	}
	if _, err := os.Stat(os.Getenv("PISTOS_CREDENTIALS_FILE")); !os.IsNotExist(err) {
		t.Fatal("declining must not save the key")
	}
}

func TestAISetupAbortsWithoutWriting(t *testing.T) {
	s := fakeProvider(t, false)
	dir := setupRepo(t)
	before, _ := os.ReadFile(filepath.Join(dir, ".pistos", "config.toml"))
	code, _, errs := runInput(t, dir, "1\n"+s.URL+"/v1\nbad\nbad\nbad\n", "ai", "setup")
	after, _ := os.ReadFile(filepath.Join(dir, ".pistos", "config.toml"))
	if code != ExitError || !strings.Contains(errs, "after 3 attempts") || !bytes.Equal(before, after) {
		t.Fatalf("three refused keys must abort without writing: %d %s", code, errs)
	}
	if code, _, errs := runInput(t, dir, "1\n", "ai", "setup"); code != ExitError || !strings.Contains(errs, "input ended") {
		t.Fatalf("ended input must abort: %d %s", code, errs)
	}
}

func TestAISetupCostEstimate(t *testing.T) {
	price := func(v float64) *float64 { return &v }
	m := config.ModelAlias{Model: "acme/gpt-mini", PromptUSDPerMTok: price(1), CompletionUSDPerMTok: price(2)}
	s := config.AISetup{
		Passes: []config.AIPass{{ID: "pass-1", Model: "m"}, {ID: "pass-2", Model: "m"}},
		Triage: config.AITriage{Mode: "model", Model: "m"},
		Models: []config.NamedModel{{Alias: "m", ModelAlias: m}},
	}
	// 3 calls × (300000/3 prompt tokens × $1/M + 1000 completion tokens × $2/M) = 3 × $0.102.
	got := reviewCostEstimate(s, config.AIBudget{MaxEvidenceChars: 300_000, MaxTokens: 1000})
	if !strings.Contains(got, "at most ~$0.3060 (an estimate") || !strings.Contains(got, "3 call(s)") {
		t.Fatalf("estimate: %s", got)
	}
}

func TestAISetupRefusesUnpricedModelsUnderAUSDCap(t *testing.T) {
	s := fakeProvider(t, false)
	dir := setupRepo(t)
	t.Setenv("PISTOS_AI_KEY", "")
	p := filepath.Join(dir, ".pistos", "config.toml")
	src, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(src), "max_candidates = 7\n", "max_candidates = 7\nmax_review_usd = 1.0\n", 1)), 0o644)
	before, _ := os.ReadFile(p)
	input := strings.Join([]string{"1", s.URL + "/v1", "good", "", "n", "1", "other/llama-3", "2"}, "\n") + "\n"
	code, out, errs := runInput(t, dir, input, "ai", "setup")
	after, _ := os.ReadFile(p)
	if code != ExitError || !strings.Contains(errs, "publishes no prices for other/llama-3") || !bytes.Equal(before, after) {
		t.Fatalf("an unpriced model under a USD cap must abort without writing: %d\n%s\n%s", code, out, errs)
	}
}
