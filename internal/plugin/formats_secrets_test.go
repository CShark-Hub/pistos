package plugin

import (
	"encoding/json"
	"strings"
	"testing"
)

// Fake secrets are assembled at run time so this file does not itself trip secret scanners.
var (
	fakeToken = "ghp_" + "PistosFakeTestToken0123456789abcdefX"
	fakeKey   = "-----BEGIN " + "RSA PRIVATE KEY-----\n  PistosFakeTestKeyDoNotUsePistosFakeTestKeyDoNotUsePistosFakeTestKey\n  -----END RSA PRIVATE KEY-----"
)

// gitleaksSample is the shape captured from gitleaks 8.30.1 (`gitleaks dir . --report-format=json
// --report-path=- --exit-code=3`, run without --redact so the parser must drop the secret itself).
func gitleaksSample() []byte {
	b, _ := json.Marshal([]map[string]any{
		{"RuleID": "github-pat", "Description": "Uncovered a GitHub Personal Access Token, potentially leading to unauthorized repository access and sensitive content exposure.",
			"StartLine": 3, "EndLine": 3, "StartColumn": 17, "EndColumn": 56, "Match": fakeToken, "Secret": fakeToken,
			"File": "main.go", "SymlinkFile": "", "Commit": "", "Entropy": 4.753056, "Author": "", "Email": "", "Date": "", "Message": "",
			"Tags": []string{}, "Fingerprint": "main.go:github-pat:3"},
		{"RuleID": "private-key", "Description": "Identified a Private Key, which may compromise cryptographic security and sensitive data encryption.",
			"StartLine": 2, "EndLine": 5, "StartColumn": 4, "EndColumn": 32, "Match": fakeKey, "Secret": fakeKey,
			"File": "cfg/deploy.yaml", "SymlinkFile": "", "Commit": "", "Entropy": 4.3418684, "Author": "", "Email": "", "Date": "", "Message": "",
			"Tags": []string{"decoded:base64", "decode-depth:1"}, "Fingerprint": "cfg/deploy.yaml:private-key:2"},
	})
	return b
}

func TestGitleaksMapsFindingsAndDropsSecrets(t *testing.T) {
	p, err := Parse("gitleaks-json", Output{ExitCode: 3, Stdout: gitleaksSample(), Stderr: []byte("8:07PM WRN leaks found: 2\n")})
	if err != nil || p.Incomplete != "" || len(p.Diagnostics) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	d := p.Diagnostics[0]
	if d.Path != "main.go" || d.Line != 3 || d.Column != 17 || d.Rule != "github-pat" || d.Severity != "error" ||
		!strings.HasPrefix(d.Message, "Uncovered a GitHub Personal Access Token") {
		t.Fatalf("mapping: %+v", d)
	}
	if k := p.Diagnostics[1]; k.Path != "cfg/deploy.yaml" || k.Line != 2 || k.EndLine != 5 || k.Rule != "private-key" {
		t.Fatalf("multi-line secrets keep their range: %+v", k)
	}
	b, _ := json.Marshal(p)
	for _, leak := range []string{"PistosFakeTest", "Fingerprint", "Entropy"} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("parsed output carries %q: %s", leak, b)
		}
	}
}

func TestGitleaksFailuresAreNotClean(t *testing.T) {
	// An invalid .gitleaks.toml makes gitleaks exit 1 with nothing on stdout.
	if _, err := Parse("gitleaks-json", Output{ExitCode: 1, Stderr: []byte("FTL unable to load gitleaks config, err: While parsing config: toml: expected character =\n")}); err == nil {
		t.Fatal("empty output must fail")
	}
	// Decoding errors must not quote the (possibly secret) output.
	_, err := Parse("gitleaks-json", Output{ExitCode: 3, Stdout: []byte(`[{"RuleID":"github-pat","Secret":"` + fakeToken + `"`)})
	if err == nil || strings.Contains(err.Error(), "PistosFakeTest") {
		t.Fatalf("truncated report: %v", err)
	}
	p, err := Parse("gitleaks-json", Output{Stdout: []byte("[]\n")})
	if err != nil || len(p.Diagnostics) != 0 || p.Incomplete != "" {
		t.Fatalf("clean run: %+v %v", p, err)
	}
}

func TestSecretsManifest(t *testing.T) {
	m, err := LoadManifest("secrets")
	if err != nil {
		t.Fatal(err)
	}
	c := m.Checks["gitleaks"]
	if m.Name != "secrets" || !c.Sensitive || c.Scope != "file" || c.ExitCodes["3"] != "findings" || c.ExitCodes["1"] != "" {
		t.Fatalf("gitleaks check: %+v", c)
	}
	if m.Tools["gitleaks"].Acquire == nil || m.Container == nil || m.Container.Tools["gitleaks"] == "" {
		t.Fatal("gitleaks must be acquirable and available in the pack image")
	}
}
