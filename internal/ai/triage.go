package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/fsutil"
)

// triage assigns an outcome to every cluster and marks repeated candidates as duplicates.
// Evidence mode needs no model; model mode asks a separate triager to refute each hypothesis.
func (e *engine) triage(ctx context.Context, clusters map[string][]int, cands []finding.Candidate) finding.AIPassStatus {
	cfg := e.in.Config.AI.Triage
	st := finding.AIPassStatus{ID: "triage", Model: cfg.Model, Status: finding.StatusComplete}
	ids := clusterIDs(clusters)
	verdicts := map[string]assessment{}
	if cfg.Mode == "model" && len(ids) > 0 {
		var err error
		verdicts, err = e.modelTriage(ctx, ids, clusters, cands)
		if err != nil {
			st.Status, st.Error = statusFor(err), err.Error()
		}
	} else if cfg.Mode != "model" {
		st.Model = "evidence"
	}

	for _, key := range ids {
		idx := clusters[key]
		lead := &cands[idx[0]]
		outcome, reason := finding.OutcomeSupported, ""
		passes := map[string]bool{}
		for _, i := range idx {
			passes[cands[i].Pass] = true
		}
		if cfg.Mode == "model" {
			switch v, ok := verdicts[key]; {
			case !ok:
				outcome, reason = finding.OutcomeNeedsInvestigation, "triage did not assess this hypothesis"
			case v.Verdict == "refuted":
				outcome, reason = finding.OutcomeRejected, "refuted by triage: "+v.Reason
			case v.Verdict == "supported":
				outcome, reason = finding.OutcomeSupported, v.Reason
			default:
				outcome, reason = finding.OutcomeNeedsInvestigation, "unresolved by triage: "+v.Reason
			}
		}
		if outcome == finding.OutcomeSupported && len(passes) == 1 && !*e.in.Config.AI.Triage.KeepSupportedSingletons {
			outcome, reason = finding.OutcomeNeedsInvestigation, "reported by a single pass; keep_supported_singletons is off"
		}
		if outcome != finding.OutcomeRejected && e.corroborated(lead) {
			outcome, reason = finding.OutcomeVerified, "a deterministic check reports a problem on the same line"
		}
		lead.Outcome, lead.Reason = outcome, reason
		for _, i := range idx[1:] {
			cands[i].Outcome, cands[i].Reason = finding.OutcomeDuplicate, "same cause as "+lead.ID
		}
	}
	return st
}

// corroborated reports trusted deterministic evidence at the candidate's anchor.
func (e *engine) corroborated(c *finding.Candidate) bool {
	for _, f := range e.in.Deterministic {
		if f.Location.Path == c.Anchors[0].Path && f.Location.Line == c.Anchors[0].Line {
			return true
		}
	}
	return false
}

type assessment struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// modelTriage hides pass identity and vote counts: the triager sees neutral ids, the hypothesis,
// its assumptions and the surrounding code only.
func (e *engine) modelTriage(ctx context.Context, ids []string, clusters map[string][]int, cands []finding.Candidate) (map[string]assessment, error) {
	system, err := e.systemPrompt("triage.md")
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	neutral := map[string]string{}
	for n, key := range ids {
		id := fmt.Sprintf("c%d", n+1)
		neutral[id] = key
		c := cands[clusters[key][0]]
		f := e.file(c.Anchors[0].Path)
		fmt.Fprintf(&b, "\n## %s\nfamily: %s\nlocation: %s:%d\nhypothesis: %s\nassumptions:\n", id, c.Family, f.Path, c.Anchors[0].Line, c.Hypothesis)
		for _, a := range c.Assumptions {
			fmt.Fprintf(&b, "- %s\n", a)
		}
		fmt.Fprintf(&b, "failing scenario: %s\ncode (untrusted data):\n", c.FailingScenario)
		from, to := max(1, c.Anchors[0].Line-25), min(len(f.Lines), c.Anchors[0].Line+25)
		for i := from; i <= to; i++ {
			fmt.Fprintf(&b, "%5d| %s\n", i, f.Lines[i-1])
		}
	}
	content, err := e.call(ctx, "triage", e.in.Config.AI.Triage.Model, []Message{{Role: "system", Content: system}, {Role: "user", Content: b.String()}}, b.String(), e.in.Refresh)
	if err != nil {
		return nil, err
	}
	if m := fenced.FindStringSubmatch(content); m != nil {
		content = m[1]
	}
	var out struct {
		Assessments []assessment `json:"assessments"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("malformed triage output: %v", err)
	}
	res := map[string]assessment{}
	for _, a := range out.Assessments {
		if key, ok := neutral[a.ID]; ok {
			res[key] = a
		}
	}
	return res, nil
}

// ledgerEntry remembers an accepted AI issue for the snapshot it was accepted on.
type ledgerEntry struct {
	Tree    string          `json:"tree"`
	Finding finding.Finding `json:"finding"`
}

type ledger struct {
	Schema  int                    `json:"schema"`
	Entries map[string]ledgerEntry `json:"entries"`
}

func accepted(outcome string) bool {
	return outcome == finding.OutcomeVerified || outcome == finding.OutcomeSupported || outcome == finding.OutcomeNeedsInvestigation
}

// mergeLedger keeps accepted issues that a refreshed run did not rediscover: omission is not
// refutation. Issues from an earlier snapshot are carried while their anchor line still exists and
// dropped once the code at the anchor changed.
func (e *engine) mergeLedger(current []finding.Finding, cands []finding.Candidate) []finding.Finding {
	path := filepath.Join(e.in.Repo, ".pistos", "state", "ai-issues.json")
	l := ledger{Schema: 1, Entries: map[string]ledgerEntry{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	refuted := map[string]bool{}
	for _, c := range cands {
		if c.Cluster != "" && c.Outcome == finding.OutcomeRejected {
			refuted[c.Cluster] = true
		}
	}
	var out []finding.Finding
	present := map[string]bool{}
	for _, f := range current {
		if accepted(f.AI.Outcome) {
			out = append(out, f)
			present[f.Key] = true
		}
	}
	for key, entry := range l.Entries {
		if present[key] || refuted[key] {
			continue
		}
		f := entry.Finding
		if entry.Tree == e.in.Tree {
			f.AI.Carried = "accepted earlier on this snapshot and not rediscovered in this run; omission does not resolve it"
			out = append(out, f)
			continue
		}
		file := e.file(f.Location.Path)
		if file == nil {
			continue
		}
		found := 0
		for i, line := range file.Lines {
			if finding.AnchorHash(line) == f.Anchor.TextHash {
				found = i + 1
				break
			}
		}
		if found == 0 {
			continue
		}
		f.Location.Line = found
		f.AI.Outcome = finding.OutcomeNeedsInvestigation
		f.AI.Carried = "carried from an earlier snapshot " + short(entry.Tree) + "; not re-verified on this one"
		out = append(out, f)
	}

	next := ledger{Schema: 1, Entries: map[string]ledgerEntry{}}
	for _, f := range out {
		next.Entries[f.Key] = ledgerEntry{Tree: e.in.Tree, Finding: f}
	}
	if b, err := json.MarshalIndent(next, "", " "); err == nil {
		_ = fsutil.WriteFileAtomic(path, b, 0o644)
	}
	finding.Sort(out)
	return out
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
