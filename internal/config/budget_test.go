package config

import (
	"strings"
	"testing"
)

func TestAIBudgetAndInstructionsValidation(t *testing.T) {
	priced := strings.Replace(valid, "timeout = \"30s\"\n", "timeout = \"30s\"\nprompt_usd_per_mtok = 0.5\ncompletion_usd_per_mtok = 0\n", 1)
	c, err := Parse([]byte(priced + "\n[ai.budget]\nmax_review_usd = 0.25\nmax_review_tokens = 1000\n\n[[ai.instructions]]\nfile = \"AGENTS.md\"\npaths = [\"internal/api/**\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.AI.Budget.Capped() || *c.Models["reviewer-a"].CompletionUSDPerMTok != 0 || c.AI.Instructions[0].Paths[0] != "internal/api/**" {
		t.Fatalf("parsed: %+v %+v", c.AI, c.Models)
	}
	for name, tc := range map[string]struct{ src, want string }{
		"unpriced alias": {valid + "\n[ai.budget]\nmax_review_usd = 1\n", "models.reviewer-a needs prompt_usd_per_mtok"},
		"negative cap":   {valid + "\n[ai.budget]\nmax_review_tokens = -1\n", "must not be negative"},
		"negative price": {strings.Replace(valid, "timeout = \"30s\"\n", "timeout = \"30s\"\nprompt_usd_per_mtok = -1\n", 1), "prices must not be negative"},
		"no file":        {valid + "\n[[ai.instructions]]\npaths = [\"x\"]\n", "ai.instructions[0].file"},
		"absolute file":  {valid + "\n[[ai.instructions]]\nfile = \"/etc/passwd\"\n", "ai.instructions[0].file"},
		"unknown key":    {valid + "\n[[ai.instructions]]\nfile = \"a.md\"\nwhen = \"x\"\n", "unknown keys"},
	} {
		if _, err := Parse([]byte(tc.src)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", name, tc.want, err)
		}
	}
}
