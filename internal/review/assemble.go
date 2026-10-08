package review

import (
	"fmt"
	"sort"
	"strings"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/plugin"
)

const (
	ChangeNew      = "new"
	ChangeExisting = "existing"
	ChangeUnknown  = "unknown"
	ChangeInScope  = "in-scope"
)

// assemble merges per-tool findings per project and classifies them against the baseline.
func assemble(results []*taskResult, s finding.Scope, manifests map[string]*plugin.Manifest, projects []finding.Project) ([]finding.CapabilityResult, []finding.Issue) {
	var caps []finding.CapabilityResult
	head := map[string][]finding.Finding{}
	baseKeys := map[string]map[string]bool{}
	baseComplete := map[string]bool{}
	for _, p := range projects {
		baseKeys[p.ID] = map[string]bool{}
		baseComplete[p.ID] = true
	}
	for _, r := range results {
		caps = append(caps, r.result)
		pid := r.task.project.ID
		switch r.task.Side {
		case sideHead:
			head[pid] = append(head[pid], r.result.Findings...)
		case sideBase:
			if r.result.Status == finding.StatusNotRequested {
				continue
			}
			if !r.result.Status.Completed() {
				baseComplete[pid] = false
			}
			for _, f := range r.result.Findings {
				baseKeys[pid][f.Key] = true
			}
		}
	}

	var issues []finding.Issue
	for _, p := range projects {
		for _, f := range finding.Merge(head[p.ID], manifests[p.Ecosystem].Authority) {
			change := ChangeInScope
			if s.Mode != "all" {
				switch {
				case baseKeys[p.ID][f.Key]:
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

// gated reports whether an issue's baseline relationship puts it under the gate.
func gated(change string) bool { return change != ChangeExisting }

// Finalize recomputes dispositions, blocking flags and the verdict. It is pure so a stored report
// can be refiltered after a feedback change without rerunning any tool or model.
func Finalize(rep *finding.Report, blockOn string, apply func(*finding.Issue)) {
	threshold := finding.Severity(blockOn).Rank()
	if blockOn == "none" {
		threshold = 99
	}
	blocking := 0
	for i := range rep.Issues {
		is := &rep.Issues[i]
		is.Disposition = finding.Disposition{State: finding.DispActive}
		if apply != nil {
			apply(is)
		}
		is.Blocking = is.Lane == finding.LaneDeterministic && gated(is.Change) &&
			is.Disposition.State != finding.DispSuppressed && is.Severity.Rank() >= threshold
		if is.Blocking {
			blocking++
		}
	}

	var incomplete []string
	for _, c := range rep.Coverage.Capabilities {
		if c.Required && !c.Status.Completed() {
			incomplete = append(incomplete, fmt.Sprintf("%s/%s (%s)", c.Project, c.Capability, c.Status))
		}
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
