package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	parsers["gitleaks-json"] = parseGitleaks
}

// gitleaksFinding declares only the fields pistos keeps. Match, Secret, Line, Fingerprint, Entropy
// and the commit metadata are deliberately absent, so the decoder drops them and no secret text
// reaches findings, the result cache or any report.
type gitleaksFinding struct {
	RuleID      string `json:"RuleID"`
	Description string `json:"Description"`
	File        string `json:"File"`
	StartLine   int    `json:"StartLine"`
	EndLine     int    `json:"EndLine"`
	StartColumn int    `json:"StartColumn"`
	EndColumn   int    `json:"EndColumn"`
}

// parseGitleaks reads `gitleaks dir --report-format=json --report-path=-`: one JSON array on stdout
// (empty when clean). Fatal errors such as an invalid .gitleaks.toml print nothing on stdout.
func parseGitleaks(out Output) (Parsed, error) {
	var p Parsed
	var leaks []gitleaksFinding
	if err := json.Unmarshal(bytes.TrimSpace(out.Stdout), &leaks); err != nil {
		return p, fmt.Errorf("gitleaks: malformed JSON report (%v); stderr: %s", jsonErrorKind(err), tail(strings.Split(string(out.Stderr), "\n")))
	}
	for _, l := range leaks {
		if l.File == "" || l.RuleID == "" {
			p.Incomplete = "gitleaks reported a finding without a file or rule"
			continue
		}
		msg := firstLine(l.Description)
		if msg == "" {
			msg = "possible secret (" + l.RuleID + ")"
		}
		p.Diagnostics = append(p.Diagnostics, Diagnostic{
			Path: l.File, Line: l.StartLine, Column: l.StartColumn, EndLine: l.EndLine, EndColumn: l.EndColumn,
			Rule: l.RuleID, Severity: "error", Message: msg,
		})
	}
	return p, nil
}

// jsonErrorKind describes a decoding error without quoting the input: a partially written report
// may contain secret text, and error messages end up in reports.
func jsonErrorKind(err error) string {
	switch e := err.(type) {
	case *json.SyntaxError:
		return fmt.Sprintf("syntax error at offset %d", e.Offset)
	case *json.UnmarshalTypeError:
		return fmt.Sprintf("unexpected %s for field %s", e.Value, e.Field)
	}
	return "unreadable output"
}
