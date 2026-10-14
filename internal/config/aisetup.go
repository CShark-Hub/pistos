package config

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AISetup is what `pistos ai setup` decided; RewriteAI turns it into config.toml sections.
type AISetup struct {
	Required     bool
	ReuseResults bool
	Passes       []AIPass
	Triage       AITriage
	// Models keeps the aliases in the order they are written.
	Models []NamedModel
}

type NamedModel struct {
	Alias string
	ModelAlias
}

var (
	headerLine  = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)
	aliasUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)
)

// ModelAliasName derives a readable alias from a provider model id ("openai/gpt-4.1" → "openai-gpt-4-1").
// Dots are replaced too: in a TOML table header they would nest tables.
func ModelAliasName(modelID string) string {
	s := strings.Trim(aliasUnsafe.ReplaceAllString(strings.ToLower(modelID), "-"), "-_")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-_")
	}
	if s == "" {
		s = "model"
	}
	return s
}

// owned reports whether a table belongs to the AI setup and is replaced wholesale. [ai.budget] is
// kept: setup never asks about budgets, so hand-tuned limits survive.
func owned(table string) bool {
	return table == "ai" || table == "ai.discovery" || table == "ai.triage" || table == "models" || strings.HasPrefix(table, "models.")
}

// RewriteAI replaces the [ai], [[ai.discovery]], [ai.triage] and [models.*] tables of a config file
// and leaves everything else byte for byte. Comments inside the replaced tables are lost.
func RewriteAI(src []byte, s AISetup) ([]byte, error) {
	lines := strings.SplitAfter(string(src), "\n")
	var out []string
	insertAt := -1
	dropping := false
	for _, l := range lines {
		if m := headerLine.FindStringSubmatch(l); m != nil {
			table := strings.ReplaceAll(m[1], " ", "")
			table = strings.ReplaceAll(table, `"`, "")
			dropping = owned(table)
			if dropping && insertAt < 0 {
				insertAt = len(out)
			}
			if !dropping && insertAt < 0 && table == "projects" {
				insertAt = len(out)
			}
		}
		if !dropping {
			out = append(out, l)
		}
	}
	if insertAt < 0 {
		insertAt = len(out)
	}
	block := RenderAI(s)
	if insertAt > 0 && !strings.HasSuffix(out[insertAt-1], "\n") {
		block = "\n" + block
	}
	if insertAt > 0 && strings.TrimSpace(out[insertAt-1]) != "" {
		block = "\n" + block
	}
	if insertAt < len(out) {
		block += "\n"
	}
	res := strings.Join(out[:insertAt], "") + block + strings.Join(out[insertAt:], "")
	c, err := Parse([]byte(res))
	if err != nil {
		return nil, fmt.Errorf("internal error: the rewritten configuration does not validate: %w", err)
	}
	if !c.AI.Enabled || len(c.AI.Discovery) != len(s.Passes) {
		return nil, fmt.Errorf("internal error: the rewritten configuration lost the AI setup")
	}
	return []byte(res), nil
}

// RenderAI renders the tables RewriteAI writes, for previews.
func RenderAI(s AISetup) string {
	var b bytes.Buffer
	b.WriteString("[ai]\n# Written by `pistos ai setup`; AI review is advisory and runs with --profile thorough.\n")
	fmt.Fprintf(&b, "enabled = true\nrequired = %t\naggregation = \"union\"\nreuse_results = %t\n", s.Required, s.ReuseResults)
	for _, p := range s.Passes {
		fmt.Fprintf(&b, "\n[[ai.discovery]]\nid = %s\nmodel = %s\n", strconv.Quote(p.ID), strconv.Quote(p.Model))
	}
	b.WriteString("\n[ai.triage]\n")
	fmt.Fprintf(&b, "mode = %s\n", strconv.Quote(s.Triage.Mode))
	if s.Triage.Mode == "model" {
		fmt.Fprintf(&b, "model = %s\n", strconv.Quote(s.Triage.Model))
	}
	for _, m := range s.Models {
		fmt.Fprintf(&b, "\n[models.%s]\nbackend = %s\nendpoint = %s\nmodel = %s\n", m.Alias,
			strconv.Quote(m.Backend), strconv.Quote(m.Endpoint), strconv.Quote(m.Model))
		if m.APIKeyEnv != "" {
			fmt.Fprintf(&b, "api_key_env = %s   # the variable's name; the key is never stored in this file\n", strconv.Quote(m.APIKeyEnv))
		}
		if m.Timeout.Duration > 0 {
			t := m.Timeout.String()
			if m.Timeout.Duration%time.Second == 0 {
				t = fmt.Sprintf("%ds", int(m.Timeout.Seconds()))
			}
			fmt.Fprintf(&b, "timeout = %s\n", strconv.Quote(t))
		}
		if m.Concurrency > 0 {
			fmt.Fprintf(&b, "concurrency = %d\n", m.Concurrency)
		}
		if m.Priced() {
			fmt.Fprintf(&b, "prompt_usd_per_mtok = %s   # from the provider's catalog; used to estimate and cap spend\ncompletion_usd_per_mtok = %s\n",
				strconv.FormatFloat(*m.PromptUSDPerMTok, 'f', -1, 64), strconv.FormatFloat(*m.CompletionUSDPerMTok, 'f', -1, 64))
		}
	}
	return b.String()
}
