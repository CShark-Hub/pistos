package config

import (
	"strings"
	"testing"
	"time"
)

func TestRewriteAIReplacesOnlyAITables(t *testing.T) {
	src := `schema = 1
languages = ["go"]

[policy]
# keep me
block_on = "warning"

[ai]
enabled = false
required = true

[[ai.discovery]]
id = "old"
model = "old-model"

[ai.budget]
max_tokens = 1234

[models.old-model]
backend = "openai-compatible"
endpoint = "https://old.example/v1"
model = "x"

[[projects]]
id = "go"
root = "."
ecosystem = "go"
checks = ["go-vet"]
`
	s := AISetup{
		Required: true, ReuseResults: true,
		Passes: []AIPass{{ID: "pass-1", Model: "m"}},
		Triage: AITriage{Mode: "model", Model: "m"},
		Models: []NamedModel{{Alias: ModelAliasName("Vendor/GPT-4.1"), ModelAlias: ModelAlias{Backend: "openai-compatible", Endpoint: "https://api.example/v1", Model: "vendor/gpt-4.1", APIKeyEnv: "K", Timeout: Duration{2 * time.Minute}, Concurrency: 2}}},
	}
	s.Passes[0].Model, s.Triage.Model = s.Models[0].Alias, s.Models[0].Alias
	out, err := RewriteAI([]byte(src), s)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"# keep me", "max_tokens = 1234", "[models.vendor-gpt-4-1]", `checks = ["go-vet"]`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old-model") || strings.Count(got, "[ai]") != 1 {
		t.Fatalf("old AI tables must be replaced:\n%s", got)
	}
	c, err := Parse(out)
	if err != nil || !c.AI.Required || c.AI.Budget.MaxTokens != 1234 || c.Models["vendor-gpt-4-1"].Model != "vendor/gpt-4.1" {
		t.Fatalf("round trip: %v %+v", err, c)
	}
}
