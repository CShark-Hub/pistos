// Command aibench compares AI review variants on the labeled corpus in testdata/ai-corpus with
// matched budgets. It sends only corpus files to the given endpoint.
//
//	go run ./tools/aibench -pistos <binary> -endpoint http://localhost:8000/v1 -model <id> [-key-env NAME] [-out docs/benchmarks]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type label struct {
	Path  string `json:"path"`
	Line  int    `json:"line"`
	Label string `json:"label"`
}

type variant struct {
	Name   string
	Passes int
	Triage string
}

type score struct {
	Variant           string  `json:"variant"`
	Labels            int     `json:"labels"`
	TruePositives     int     `json:"true_positives"`
	FalsePositives    int     `json:"false_positives"`
	Recall            float64 `json:"recall"`
	Precision         float64 `json:"precision"`
	Singletons        int     `json:"accepted_singletons"`
	FalseRejections   int     `json:"true_defects_rejected_by_triage"`
	RawCandidates     int     `json:"raw_candidates"`
	Calls             int     `json:"model_calls"`
	PromptTokens      int     `json:"prompt_tokens"`
	CompletionTokens  int     `json:"completion_tokens"`
	Seconds           float64 `json:"wall_seconds"`
	IncompleteReviews int     `json:"incomplete_ai_reviews"`
}

type envelope struct {
	Canonical struct {
		Issues []struct {
			Lane     string `json:"lane"`
			Location struct {
				Path string `json:"path"`
				Line int    `json:"line"`
			} `json:"location"`
			AI *struct {
				Passes []string `json:"passes"`
			} `json:"ai"`
		} `json:"issues"`
		AI *struct {
			Status     string `json:"status"`
			Candidates []struct {
				Anchors []struct {
					Path string `json:"path"`
					Line int    `json:"line"`
				} `json:"anchors"`
				Outcome string `json:"outcome"`
				Reason  string `json:"reason"`
			} `json:"candidates"`
		} `json:"ai"`
	} `json:"canonical"`
	Telemetry struct {
		AI []struct {
			Cached           bool `json:"cached"`
			PromptTokens     int  `json:"prompt_tokens"`
			CompletionTokens int  `json:"completion_tokens"`
		} `json:"ai"`
	} `json:"telemetry"`
}

func main() {
	pistos := flag.String("pistos", "", "pistos binary")
	endpoint := flag.String("endpoint", "", "OpenAI-compatible endpoint")
	model := flag.String("model", "", "model identifier")
	keyEnv := flag.String("key-env", "", "environment variable holding the API key, if any")
	out := flag.String("out", "", "directory for ai-results.json and ai-results.md")
	flag.Parse()
	if *pistos == "" || *endpoint == "" || *model == "" {
		fmt.Fprintln(os.Stderr, "-pistos, -endpoint and -model are required")
		os.Exit(2)
	}
	bin, _ := filepath.Abs(*pistos)
	root, _ := os.Getwd()
	corpus := filepath.Join(root, "testdata", "ai-corpus")
	cases, _ := os.ReadDir(corpus)
	cache, _ := os.MkdirTemp("", "pistos-aibench-")
	defer os.RemoveAll(cache)
	os.Setenv("PISTOS_CACHE_DIR", cache)
	os.Setenv("PISTOS_TOOLS_DIR", filepath.Join(cache, "tools"))

	variants := []variant{{"single pass", 1, "evidence"}, {"3 passes, same model", 3, "evidence"}, {"3 passes + model triage", 3, "model"}}
	var scores []score
	for _, v := range variants {
		s := score{Variant: v.Name}
		start := time.Now()
		for _, c := range cases {
			if !c.IsDir() {
				continue
			}
			fmt.Fprintf(os.Stderr, "%s / %s\n", v.Name, c.Name())
			evaluate(bin, filepath.Join(corpus, c.Name()), v, *endpoint, *model, *keyEnv, &s)
		}
		s.Seconds = math.Round(time.Since(start).Seconds())
		if s.Labels > 0 {
			s.Recall = round(float64(s.TruePositives) / float64(s.Labels))
		}
		if s.TruePositives+s.FalsePositives > 0 {
			s.Precision = round(float64(s.TruePositives) / float64(s.TruePositives+s.FalsePositives))
		}
		scores = append(scores, s)
	}
	b, _ := json.MarshalIndent(map[string]any{"endpoint_model": *model, "date": time.Now().UTC().Format("2006-01-02"), "variants": scores}, "", "  ")
	if *out == "" {
		os.Stdout.Write(b)
		return
	}
	os.WriteFile(filepath.Join(*out, "ai-results.json"), append(b, '\n'), 0o644)
	var md strings.Builder
	fmt.Fprintf(&md, "Model: `%s` (all variants, temperature 0, max_tokens 2048, identical evidence)\n\n", *model)
	md.WriteString("| Variant | Recall | Precision | TP | FP | Accepted singletons | True defects rejected by triage | Raw candidates | Calls | Prompt / completion tokens | Wall time |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range scores {
		fmt.Fprintf(&md, "| %s | %.2f | %.2f | %d/%d | %d | %d | %d | %d | %d | %d / %d | %.0f s |\n", s.Variant, s.Recall, s.Precision, s.TruePositives, s.Labels,
			s.FalsePositives, s.Singletons, s.FalseRejections, s.RawCandidates, s.Calls, s.PromptTokens, s.CompletionTokens, s.Seconds)
	}
	os.WriteFile(filepath.Join(*out, "ai-results.md"), []byte(md.String()), 0o644)
}

func evaluate(bin, caseDir string, v variant, endpoint, model, keyEnv string, s *score) {
	var labels []label
	b, _ := os.ReadFile(filepath.Join(caseDir, "labels.json"))
	_ = json.Unmarshal(b, &labels)
	s.Labels += len(labels)

	dir, _ := os.MkdirTemp("", "pistos-aibench-repo-")
	defer os.RemoveAll(dir)
	eco := "go"
	files, _ := os.ReadDir(caseDir)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".py") {
			eco = "python"
		}
	}
	os.MkdirAll(filepath.Join(dir, ".pistos"), 0o755)
	if eco == "go" {
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module corpus\n\ngo 1.24\n"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, ".pistos", ".gitignore"), []byte("cache/\nstate/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".pistos", "config.toml"), []byte(config(eco, v, endpoint, model, keyEnv)), 0o644)
	run(dir, "git", "init", "-q", "-b", "main")
	run(dir, bin, "plugins", "install")
	run(dir, "git", "add", "-A")
	run(dir, "git", "-c", "user.name=b", "-c", "user.email=b@b", "commit", "-qm", "base")
	for _, f := range files {
		if f.Name() != "labels.json" {
			data, _ := os.ReadFile(filepath.Join(caseDir, f.Name()))
			os.WriteFile(filepath.Join(dir, f.Name()), data, 0o644)
		}
	}
	out := run(dir, bin, "review", "--profile", "thorough", "--refresh-ai", "--format", "json")
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || env.Canonical.AI == nil {
		s.IncompleteReviews++
		return
	}
	if env.Canonical.AI.Status != "complete" {
		s.IncompleteReviews++
	}
	matches := func(path string, line int) int {
		for i, l := range labels {
			if l.Path == path && abs(l.Line-line) <= 3 {
				return i
			}
		}
		return -1
	}
	found := map[int]bool{}
	for _, is := range env.Canonical.Issues {
		if is.Lane != "ai" {
			continue
		}
		if i := matches(is.Location.Path, is.Location.Line); i >= 0 {
			found[i] = true
		} else {
			s.FalsePositives++
		}
		if is.AI != nil && len(is.AI.Passes) == 1 && v.Passes > 1 {
			s.Singletons++
		}
	}
	s.TruePositives += len(found)
	for _, c := range env.Canonical.AI.Candidates {
		s.RawCandidates++
		if c.Outcome == "rejected" && strings.Contains(c.Reason, "refuted") && len(c.Anchors) > 0 && matches(c.Anchors[0].Path, c.Anchors[0].Line) >= 0 {
			s.FalseRejections++
		}
	}
	for _, t := range env.Telemetry.AI {
		if !t.Cached {
			s.Calls++
		}
		s.PromptTokens += t.PromptTokens
		s.CompletionTokens += t.CompletionTokens
	}
}

func config(eco string, v variant, endpoint, model, keyEnv string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema = 1\nlanguages = [%q]\n\n[ai]\nenabled = true\n\n[ai.budget]\nmax_tokens = 2048\n", eco)
	for i := 1; i <= v.Passes; i++ {
		fmt.Fprintf(&b, "\n[[ai.discovery]]\nid = \"pass-%d\"\nmodel = \"m\"\n", i)
	}
	fmt.Fprintf(&b, "\n[ai.triage]\nmode = %q\n", v.Triage)
	if v.Triage == "model" {
		b.WriteString("model = \"m\"\n")
	}
	fmt.Fprintf(&b, "\n[models.m]\nbackend = \"openai-compatible\"\nendpoint = %q\nmodel = %q\ntimeout = \"600s\"\nmax_tokens = 2048\nconcurrency = 3\n", endpoint, model)
	if keyEnv != "" {
		fmt.Fprintf(&b, "api_key_env = %q\n", keyEnv)
	}
	fmt.Fprintf(&b, "\n[[projects]]\nid = \"p\"\nroot = \".\"\necosystem = %q\nchecks = [\"metrics\"]\n", eco)
	return b.String()
}

func run(dir string, name string, args ...string) []byte {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func round(f float64) float64 { return math.Round(f*100) / 100 }
