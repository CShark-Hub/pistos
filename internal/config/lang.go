package config

import (
	"fmt"
	"strings"
)

// Ecosystem identifiers double as language pack names.
const (
	Go     = "go"
	Python = "python"
	JSTS   = "jsts"
	CSharp = "csharp"
	// Secrets is a repository-wide pack rather than a language; it is listed in `languages` like
	// one so configurations stay uniform.
	Secrets = "secrets"
)

var Ecosystems = []string{CSharp, Go, JSTS, Python}

var languageAliases = map[string]string{
	"go": Go, "golang": Go,
	"py": Python, "python": Python,
	"js": JSTS, "javascript": JSTS, "ts": JSTS, "typescript": JSTS, "jsts": JSTS,
	"cs": CSharp, "csharp": CSharp, "c#": CSharp,
	"secrets": Secrets, "secret": Secrets,
}

// NormalizeLanguage maps a user-facing language alias to its ecosystem/pack name.
func NormalizeLanguage(s string) (string, error) {
	if eco, ok := languageAliases[strings.ToLower(strings.TrimSpace(s))]; ok {
		return eco, nil
	}
	return "", fmt.Errorf("unknown language %q (use go, python, js, ts, csharp or the secrets pack; quote \"c#\" in shells)", s)
}
