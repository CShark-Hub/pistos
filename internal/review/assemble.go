package review

import (
	"fmt"
	"sort"
	"strings"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/plugin"
)

const (
	ChangeNew      = "new"
	ChangeExisting = "existing"
	ChangeUnknown  = "unknown"
	ChangeInScope  = "in-scope"
	ChangeWorsened = "worsened"
)

// assemble merges per-tool findings per project and classifies them against the baseline.
// Baseline findings in renamed files are re-keyed to their new path first, and a finding whose
// enclosing symbol moved to another file is matched by symbol and anchor, so relocation is not
// reported as new debt.
func assemble(results []*taskResult, s finding.Scope, manifests map[string]*plugin.Manifest, projects []finding.Project, renames map[string]string) ([]finding.CapabilityResult, []finding.Issue) {
	var caps []finding.CapabilityResult
	head := map[string][]finding.Finding{}
	base := map[string]map[string]finding.Finding{}
	relocated := map[string]map[string]finding.Finding{}
	baseComplete := map[string]bool{}
	for _, p := range projects {
		base[p.ID] = map[string]finding.Finding{}
		relocated[p.ID] = map[string]finding.Finding{}
		baseComplete[p.ID] = true
	}
	for _, r := range results {
		dropFiltered(r)
		caps = append(caps, r.result)
		pid := r.task.project.ID
		switch r.task.Side {
		case sideHead:
			head[pid] = append(head[pid], r.result.Findings...)
		case sideBase:
			if r.result.Status == finding.StatusNotRequested || r.result.Status == finding.StatusUnsupported {
				continue
			}
			if !r.result.Status.Completed() {
				baseComplete[pid] = false
			}
			for _, f := range r.result.Findings {
				if to, ok := renames[f.Location.Path]; ok {
					f.Location.Path = to
					f.Key = finding.ComputeKey(f.Project, f.Family, to, f.Anchor.Symbol, f.Anchor.TextHash, f.Anchor.Ordinal)
				}
				base[pid][f.Key] = f
				if f.Anchor.Symbol != "" {
					relocated[pid][relocationKey(f)] = f
				}
			}
		}
	}

	var issues []finding.Issue
	for _, p := range projects {
		for _, f := range finding.Merge(head[p.ID], manifests[p.Ecosystem].Authority) {
			change := ChangeInScope
			if s.Mode != "all" {
				prev, ok := base[p.ID][f.Key]
				if !ok && f.Anchor.Symbol != "" {
					prev, ok = relocated[p.ID][relocationKey(f)]
				}
				switch {
				case ok && f.Metric != nil && prev.Metric != nil:
					v := prev.Metric.Value
					f.Metric.Base = &v
					change = ChangeExisting
					if f.Metric.Value > v {
						change = ChangeWorsened
					}
				case ok:
					change = ChangeExisting
				case baseComplete[p.ID]:
					change = ChangeNew
				default:
					change = ChangeUnknown
				}
			}
			issues = append(issues, finding.Issue{Finding: f, Change: change})
		}
	}
	return caps, issues
}

// dropFiltered removes findings in paths the project's include/exclude globs leave out. Tools still
// analyze the whole project, so this runs after caching: changing the globs never reruns a tool.
func dropFiltered(r *taskResult) {
	f := projectFilter(r.task.project)
	if !f.Active() {
		return
	}
	var kept []finding.Finding
	for _, fd := range r.result.Findings {
		if !f.Drops(fd.Location.Path) {
			kept = append(kept, fd)
		}
	}
	if n := len(r.result.Findings) - len(kept); n > 0 && r.task.Side == sideHead {
		r.result.Limitations = append(append([]string(nil), r.result.Limitations...),
			fmt.Sprintf("%d finding(s) dropped by the project's path filters (%s)", n, f))
	}
	r.result.Findings = kept
}

func projectFilter(p finding.Project) config.PathFilter {
	return config.PathFilter{Root: p.Root, Include: p.Include, Exclude: p.Exclude}
}

func relocationKey(f finding.Finding) string {
	return f.Family + "\x00" + f.Anchor.Symbol + "\x00" + f.Anchor.TextHash + "\x00" + fmt.Sprint(f.Anchor.Ordinal)
}

// gated reports whether an issue's baseline relationship puts it under the gate.
func gated(change string) bool { return change != ChangeExisting }

// Finalize recomputes dispositions, blocking flags and the verdict. It is pure so a stored report
// can be refiltered after a feedback change without rerunning any tool or model.
func Finalize(rep *finding.Report, blockOn string, apply func([]finding.Issue)) {
	threshold := finding.Severity(blockOn).Rank()
	if blockOn == "none" {
		threshold = 99
	}
	for i := range rep.Issues {
		rep.Issues[i].Disposition = finding.Disposition{State: finding.DispActive}
	}
	if apply != nil {
		apply(rep.Issues)
	}
	blocking := 0
	for i := range rep.Issues {
		is := &rep.Issues[i]
		// A decision whose guard no longer holds was invalidated by this change, so it gates even when
		// the finding itself pre-dates the baseline.
		underGate := gated(is.Change) || is.Disposition.State == finding.DispRevalidate
		is.Blocking = is.Lane == finding.LaneDeterministic && underGate && !is.Advisory &&
			is.Disposition.State != finding.DispSuppressed && is.Severity.Rank() >= threshold
		if is.Blocking {
			blocking++
		}
	}

	if rep.AI != nil {
		ignored := map[string]bool{}
		for _, is := range rep.Issues {
			if is.AI != nil && is.Disposition.State == finding.DispSuppressed {
				ignored[is.Key] = true
			}
		}
		for i := range rep.AI.Candidates {
			c := &rep.AI.Candidates[i]
			c.Ignored = c.Cluster != "" && ignored[c.Cluster]
		}
	}

	var incomplete []string
	for _, c := range rep.Coverage.Capabilities {
		if c.Required && !c.Status.Completed() {
			incomplete = append(incomplete, fmt.Sprintf("%s/%s (%s)", c.Project, c.Capability, c.Status))
		}
	}
	// ai.required makes an incomplete AI plan an incomplete review; AI issues still never block.
	if rep.AI != nil && rep.AI.Required && rep.AI.Status != finding.StatusComplete {
		incomplete = append(incomplete, "ai (required, "+string(rep.AI.Status)+")")
	}
	sort.Strings(incomplete)
	v := finding.Verdict{Blocking: blocking}
	switch {
	case len(incomplete) > 0:
		v.Outcome, v.ExitCode = "incomplete", 2
		v.Reason = "required checks did not complete: " + strings.Join(incomplete, ", ")
	case blocking > 0:
		v.Outcome, v.ExitCode = "blocking", 1
		v.Reason = fmt.Sprintf("%d active blocking issue(s) at or above %s", blocking, blockOn)
	case rep.Scope.Empty:
		v.Outcome, v.Reason = "pass", "no changes between base and head; nothing was analyzed"
	case len(rep.Scope.Projects) == 0:
		v.Outcome, v.Reason = "pass", "changes touch no configured project; nothing was analyzed"
	default:
		v.Outcome, v.Reason = "pass", "required deterministic checks completed with no active blocking issues"
	}
	rep.Verdict = v
	rep.Normalize()
}
