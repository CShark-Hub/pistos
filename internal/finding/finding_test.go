package finding

import (
	"bytes"
	"testing"
)

func sample(line int, msg, tool string) Finding {
	return Finding{
		Lane: LaneDeterministic, Project: "go", Family: "go-vet/printf", Severity: SevWarning,
		Message: msg, Location: Location{Path: "a.go", Line: line, Column: 5},
		Anchor:  Anchor{Symbol: "F", TextHash: AnchorHash("\treturn fmt.Sprintf(\"%s\", n)")},
		Sources: []Source{{Tool: tool, Rule: "printf", Severity: SevWarning, Message: msg}},
	}
}

func TestKeyIgnoresLineMessageAndTool(t *testing.T) {
	a := []Finding{sample(10, "wrong type", "go-vet")}
	b := []Finding{sample(42, "Sprintf arg has wrong type (reworded)", "staticcheck")}
	AssignIdentity(a)
	AssignIdentity(b)
	if a[0].Key != b[0].Key {
		t.Fatalf("line/message/tool changed identity: %s vs %s", a[0].Key, b[0].Key)
	}
}

func TestAnchorIgnoresWhitespaceOnly(t *testing.T) {
	if AnchorHash("  x :=  f( a,b )") != AnchorHash("x := f( a,b )\t") {
		t.Fatal("indentation/spacing must not change the anchor")
	}
	if AnchorHash("x := f(a)") == AnchorHash("x := f(b)") {
		t.Fatal("different code must change the anchor")
	}
}

func TestSameCauseTwiceInOneSymbolGetsDistinctKeys(t *testing.T) {
	fs := []Finding{sample(20, "m", "go-vet"), sample(10, "m", "go-vet")}
	AssignIdentity(fs)
	if fs[0].Key == fs[1].Key || fs[0].Anchor.Ordinal != 0 || fs[0].Location.Line != 10 {
		t.Fatalf("ordinals must follow position and disambiguate: %+v", fs)
	}
}

func TestMergeKeepsAllSourcesAndAuthority(t *testing.T) {
	a := []Finding{sample(10, "vet says", "go-vet")}
	b := []Finding{sample(10, "staticcheck says", "staticcheck")}
	b[0].Severity = SevError
	b[0].Sources[0].Severity = SevError
	AssignIdentity(a)
	AssignIdentity(b)
	m := Merge(append(b, a...), []string{"go-vet", "staticcheck"})
	if len(m) != 1 || len(m[0].Sources) != 2 {
		t.Fatalf("want one merged finding with two sources: %+v", m)
	}
	if m[0].Message != "vet says" || m[0].Sources[0].Tool != "go-vet" {
		t.Fatalf("authority not applied: %+v", m[0])
	}
	if m[0].Severity != SevError {
		t.Fatal("severity disagreement must keep the stronger severity visible")
	}
}

func TestCanonicalJSONStable(t *testing.T) {
	mk := func(order []int) []byte {
		var fs []Finding
		for _, l := range order {
			fs = append(fs, sample(l, "m", "go-vet"))
			fs[len(fs)-1].Anchor.TextHash = AnchorHash(string(rune('a' + l)))
		}
		AssignIdentity(fs)
		r := &Report{Schema: SchemaVersion, Scope: Scope{ChangedFiles: []string{"b", "a"}}}
		for _, f := range fs {
			r.Issues = append(r.Issues, Issue{Finding: f, Change: "new"})
		}
		r.Normalize()
		b, err := CanonicalJSON(r)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := mk([]int{3, 1, 2})
	for _, order := range [][]int{{1, 2, 3}, {2, 3, 1}} {
		if got := mk(order); !bytes.Equal(first, got) {
			t.Fatalf("canonical output depends on input order:\n%s\n%s", first, got)
		}
	}
}
