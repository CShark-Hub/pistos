package config

import (
	"os"
	"strings"
	"testing"
)

const valid = `
schema = 1
languages = ["go", "py"]
profile = "balanced"

[execution]
mode = "native"
jobs = 4

[ai]
enabled = true
required = false
aggregation = "union"

[[ai.discovery]]
id = "pass-1"
model = "reviewer-a"

[ai.triage]
mode = "model"
model = "reviewer-a"

[models.reviewer-a]
backend = "openai-compatible"
endpoint = "http://localhost:8080/v1"
model = "local-model"
api_key_env = "PISTOS_TEST_KEY"
timeout = "30s"

[[projects]]
id = "go"
root = "."
ecosystem = "golang"
checks = ["go-vet"]
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ActiveLanguages(); strings.Join(got, ",") != "go,python" {
		t.Fatalf("languages = %v", got)
	}
	if c.Models["reviewer-a"].Timeout.Seconds() != 30 || !*c.AI.ReuseResults {
		t.Fatalf("defaults/durations not applied: %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	for name, tc := range map[string]struct{ edit, want string }{
		"unknown key":      {"[policy]\nblock_on = \"error\"\nfail_fast = true\n", "unknown keys: policy.fail_fast"},
		"secret in config": {"", "api_key_env to the name of an environment variable"},
		"schema":           {"", "schema = 2 is not supported"},
		"remote http":      {"", "must use https"},
		"bad profile":      {"", "profile \"quick\""},
		"bad root":         {"", "inside the repository"},
		"unknown model":    {"", "not a defined [models.*] alias"},
		"llm vocabulary":   {"[llm]\nenabled = true\n", "unknown keys: llm"},
		"escaping glob":    {"", "projects[0].exclude"},
		"bad glob syntax":  {"", "projects[0].include"},
		"unknown hook key": {"[hooks]\npre_commit = \"fast\"\n", "unknown keys: hooks.pre_commit"},
		"bad hook profile": {"[hooks]\npre_push_profile = \"slow\"\n", `hooks.pre_push_profile "slow"`},
	} {
		t.Run(name, func(t *testing.T) {
			src := valid + tc.edit
			switch name {
			case "secret in config":
				src = strings.Replace(src, `api_key_env = "PISTOS_TEST_KEY"`, `api_key = "sk-live-123"`, 1)
			case "schema":
				src = strings.Replace(src, "schema = 1", "schema = 2", 1)
			case "remote http":
				src = strings.Replace(src, "http://localhost:8080/v1", "http://models.example.com/v1", 1)
			case "bad profile":
				src = strings.Replace(src, `profile = "balanced"`, `profile = "quick"`, 1)
			case "bad root":
				src = strings.Replace(src, `root = "."`, `root = "../elsewhere"`, 1)
			case "escaping glob":
				src = strings.Replace(src, `checks = ["go-vet"]`, "checks = [\"go-vet\"]\nexclude = [\"../other/**\"]", 1)
			case "bad glob syntax":
				src = strings.Replace(src, `checks = ["go-vet"]`, "checks = [\"go-vet\"]\ninclude = [\"src/[a\"]", 1)
			case "unknown model":
				src = strings.Replace(src, `model = "reviewer-a"`, `model = "missing"`, 1)
			}
			_, err := Parse([]byte(src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestHooksDefaults(t *testing.T) {
	c, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if h := c.Hooks; h.PreCommitProfile != "fast" || h.PrePushProfile != "balanced" || h.ClaudeProfile != "fast" || h.BlockOnIncomplete {
		t.Fatalf("hooks defaults: %+v", h)
	}
}

func TestLanguageAliases(t *testing.T) {
	for in, want := range map[string]string{"js": JSTS, "TypeScript": JSTS, "py": Python, "golang": Go, "c#": CSharp, "cs": CSharp} {
		if got, err := NormalizeLanguage(in); err != nil || got != want {
			t.Errorf("%s -> %s, %v", in, got, err)
		}
	}
	if _, err := NormalizeLanguage("rust"); err == nil {
		t.Error("rust should be rejected")
	}
}

func TestRenderedConfigRoundTrips(t *testing.T) {
	src := RenderConfig([]string{Go, CSharp}, []Project{{ID: "go", Root: ".", Ecosystem: Go, Checks: []string{"go-vet"}}})
	if _, err := Parse([]byte(src)); err != nil {
		t.Fatalf("init output must validate: %v\n%s", err, src)
	}
}

func TestSampleConfigValidates(t *testing.T) {
	b, err := os.ReadFile("../../docs/schemas/config.sample.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}
