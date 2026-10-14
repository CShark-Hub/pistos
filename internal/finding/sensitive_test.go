package finding

import "testing"

func TestSensitiveLinesCoverRangesOfSensitiveFindingsOnly(t *testing.T) {
	issues := []Issue{
		{Finding: Finding{Family: "gitleaks/private-key", Sensitive: true, Location: Location{Path: "k.yaml", Line: 2, EndLine: 4}}},
		{Finding: Finding{Family: "go-vet/printf", Location: Location{Path: "main.go", Line: 3}}},
	}
	got := SensitiveLines(issues)
	if len(got) != 1 || len(got["k.yaml"]) != 3 || got["k.yaml"][4] != "gitleaks/private-key" {
		t.Fatalf("%v", got)
	}
	lines := []string{"key: |", "a", "b", "c", "other"}
	masked := WithholdLines(lines, got["k.yaml"], func(f string) string { return "[" + f + "]" })
	if masked[0] != "key: |" || masked[1] != "[gitleaks/private-key]" || masked[3] != "[gitleaks/private-key]" || masked[4] != "other" || lines[1] != "a" {
		t.Fatalf("masked %q, original %q", masked, lines)
	}
}

func TestSensitiveIsNotIdentity(t *testing.T) {
	a := []Finding{{Project: "p", Family: "f", Location: Location{Path: "a", Line: 1}, Anchor: Anchor{TextHash: "h"}, Sources: []Source{{Tool: "t"}}}}
	b := []Finding{a[0]}
	b[0].Sensitive = true
	AssignIdentity(a)
	AssignIdentity(b)
	if a[0].Key != b[0].Key {
		t.Fatal("the sensitive flag must not change the key")
	}
	if m := Merge(append(a, b...), nil); len(m) != 1 || !m[0].Sensitive {
		t.Fatalf("merging keeps the sensitive flag: %+v", m)
	}
}
