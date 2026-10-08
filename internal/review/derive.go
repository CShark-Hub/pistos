package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"example.com/pistos/internal/facts"
	"example.com/pistos/internal/finding"
)

// deriveFindings applies the current metric/architecture rules to a facts result. Facts are cached
// without findings, so changing thresholds or layers never needs re-extraction.
func (e *engine) deriveFindings(ctx context.Context, t task, workRoot string, res *finding.CapabilityResult) {
	if res.Facts == nil {
		return
	}
	text := func(p string, line int) string {
		abs := filepath.Join(workRoot, filepath.FromSlash(p))
		return e.sources.get(abs, t.project.Ecosystem, func() ([]byte, error) { return os.ReadFile(abs) }).line(line)
	}
	var out []finding.Finding
	switch e.manifests[t.project.Ecosystem].Checks[t.Capability].Facts {
	case "metrics":
		m := e.cfg.Metrics
		out = facts.MetricFindings(res.Facts, t.project.ID, t.Capability, facts.MetricRules{Gate: m.Gate, Thresholds: map[string]int{
			"cyclomatic": m.Cyclomatic, "nesting": m.Nesting, "params": m.Params, "statements": m.Statements,
		}}, text)
	case "imports":
		a := e.cfg.Architecture
		rules := facts.ArchRules{Gate: a.Gate, Cycles: *a.Cycles, RequireComplete: a.RequireComplete}
		for _, l := range a.Layers {
			rules.Layers = append(rules.Layers, facts.Layer{Name: l.Name, Paths: l.Paths})
		}
		for _, r := range a.Rules {
			rules.Rules = append(rules.Rules, facts.LayerRule{From: r.From, Deny: r.Deny})
		}
		var unknown int
		out, unknown = facts.ArchFindings(res.Facts, t.project.ID, t.Capability, rules, text)
		res.Limitations = append(res.Limitations, fmt.Sprintf("dependency graph: %d edges, %d unresolved", len(res.Facts.Imports), unknown))
		if unknown > 0 {
			res.Limitations = append(res.Limitations, "unresolved imports exist: the absence of cycle or layer violations is not proven")
			if a.RequireComplete && res.Status.Completed() {
				res.Status = finding.StatusIncomplete
				res.Error = fmt.Sprintf("%d unresolved imports and architecture.require_complete is set", unknown)
			}
		}
	}
	for i := range out {
		out[i].Reproduce = []string{"pistos review --all  # " + t.Capability + " facts (" + res.Facts.Algorithm + ")"}
	}
	finding.AssignIdentity(out)
	for i := range out {
		out[i].RefreshEvidenceHash()
	}
	res.Findings = out
}
