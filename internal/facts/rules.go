package facts

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/fsutil"
)

type MetricRules struct {
	Gate       bool
	Thresholds map[string]int
}

var MetricNames = []string{"cyclomatic", "nesting", "params", "statements"}

func metricValue(f finding.FunctionFact, name string) int {
	switch name {
	case "cyclomatic":
		return f.Cyclomatic
	case "nesting":
		return f.Nesting
	case "params":
		return f.Params
	default:
		return f.Statements
	}
}

// LineText returns the source line used as a finding's anchor.
type LineText func(path string, line int) string

// MetricFindings reports functions above their thresholds. Whether that is new or worsened debt is
// decided against the baseline, not here.
func MetricFindings(facts *finding.Facts, project, capability string, r MetricRules, text LineText) []finding.Finding {
	var out []finding.Finding
	for _, fn := range facts.Functions {
		for _, name := range MetricNames {
			limit, ok := r.Thresholds[name]
			v := metricValue(fn, name)
			if !ok || limit <= 0 || v <= limit {
				continue
			}
			sev := finding.SevInfo
			if r.Gate {
				sev = finding.SevWarning
			}
			msg := fmt.Sprintf("%s of %s is %d (threshold %d, %s)", metricLabel(name), fn.Symbol, v, limit, facts.Algorithm)
			out = append(out, finding.Finding{
				Lane: finding.LaneDeterministic, Project: project, Family: "metrics/" + name, Severity: sev,
				Message: msg, Capability: capability, Advisory: !r.Gate,
				Location: finding.Location{Path: fn.Path, Line: fn.Line, EndLine: fn.EndLine},
				Anchor:   finding.Anchor{Symbol: fn.Symbol, TextHash: finding.AnchorHash(text(fn.Path, fn.Line))},
				Sources:  []finding.Source{{Tool: capability, Rule: name, Severity: sev, Message: msg}},
				Metric:   &finding.MetricValue{Name: name, Value: v, Threshold: limit, Algorithm: facts.Algorithm},
			})
		}
	}
	return out
}

func metricLabel(name string) string {
	switch name {
	case "cyclomatic":
		return "cyclomatic complexity"
	case "nesting":
		return "nesting depth"
	case "params":
		return "parameter count"
	}
	return "statement count"
}

type Layer struct {
	Name  string
	Paths []string
}

type LayerRule struct {
	From string
	Deny []string
}

type ArchRules struct {
	Gate            bool
	Cycles          bool
	RequireComplete bool
	Layers          []Layer
	Rules           []LayerRule
}

func (a ArchRules) layerOf(node string) string {
	for _, l := range a.Layers {
		for _, g := range l.Paths {
			if fsutil.MatchGlob(g, node) || fsutil.MatchGlob(strings.TrimSuffix(g, "/**"), node) {
				return l.Name
			}
		}
	}
	return ""
}

// ArchFindings applies layer and cycle rules to import edges. Unknown edges are counted: they leave
// the graph incomplete, so a clean result does not prove the absence of violations.
func ArchFindings(facts *finding.Facts, project, capability string, a ArchRules, text LineText) (out []finding.Finding, unknown int) {
	node := func(p string) string {
		if facts.Nodes == finding.NodesDir {
			return path.Dir(p)
		}
		return p
	}
	sev := finding.SevInfo
	if a.Gate {
		sev = finding.SevWarning
	}
	mk := func(family string, e finding.ImportFact, msg string) finding.Finding {
		return finding.Finding{
			Lane: finding.LaneDeterministic, Project: project, Family: family, Severity: sev, Message: msg,
			Capability: capability, Advisory: !a.Gate,
			Location: finding.Location{Path: e.From, Line: e.Line},
			Anchor:   finding.Anchor{TextHash: finding.AnchorHash(text(e.From, e.Line))},
			Sources:  []finding.Source{{Tool: capability, Rule: strings.TrimPrefix(family, "arch/"), Severity: sev, Message: msg}},
		}
	}

	graph := map[string]map[string]finding.ImportFact{}
	for _, e := range facts.Imports {
		if e.To == "" || e.Provenance == finding.ProvenanceUnknown {
			unknown++
			continue
		}
		from, to := node(e.From), e.To
		if facts.Nodes == finding.NodesFile {
			to = e.To
		}
		if from == to {
			continue
		}
		if graph[from] == nil {
			graph[from] = map[string]finding.ImportFact{}
		}
		if prev, ok := graph[from][to]; !ok || lessEdge(e, prev) {
			graph[from][to] = e
		}
		fl, tl := a.layerOf(from), a.layerOf(to)
		for _, r := range a.Rules {
			if r.From != fl || fl == "" {
				continue
			}
			for _, d := range r.Deny {
				if d == tl {
					out = append(out, mk("arch/layer", e, fmt.Sprintf("layer %s must not depend on %s: %s imports %s", fl, tl, e.From, e.Spec)))
				}
			}
		}
	}
	if a.Cycles {
		for _, scc := range stronglyConnected(graph) {
			e, ok := smallestEdge(graph, scc)
			if !ok {
				continue
			}
			out = append(out, mk("arch/cycle", e, "dependency cycle: "+strings.Join(cyclePath(graph, scc), " → ")))
		}
	}
	return out, unknown
}

func lessEdge(a, b finding.ImportFact) bool {
	if a.From != b.From {
		return a.From < b.From
	}
	return a.Line < b.Line
}

// smallestEdge anchors a cycle deterministically at its lexicographically first internal edge.
func smallestEdge(g map[string]map[string]finding.ImportFact, scc []string) (finding.ImportFact, bool) {
	in := map[string]bool{}
	for _, n := range scc {
		in[n] = true
	}
	var best finding.ImportFact
	found := false
	for _, n := range scc {
		for to, e := range g[n] {
			if in[to] && (!found || lessEdge(e, best)) {
				best, found = e, true
			}
		}
	}
	return best, found
}

// cyclePath renders one concrete cycle through the component, starting from its smallest node.
func cyclePath(g map[string]map[string]finding.ImportFact, scc []string) []string {
	in := map[string]bool{}
	for _, n := range scc {
		in[n] = true
	}
	start := scc[0]
	pathNodes := []string{start}
	seen := map[string]bool{start: true}
	cur := start
	for {
		var next []string
		for to := range g[cur] {
			if in[to] {
				next = append(next, to)
			}
		}
		sort.Strings(next)
		moved := false
		for _, n := range next {
			if n == start && len(pathNodes) > 1 {
				return append(pathNodes, start)
			}
			if !seen[n] {
				seen[n] = true
				pathNodes = append(pathNodes, n)
				cur = n
				moved = true
				break
			}
		}
		if !moved {
			for _, n := range next {
				if n == start {
					return append(pathNodes, start)
				}
			}
			return append(pathNodes, "…")
		}
	}
}

// stronglyConnected returns components with more than one node, each sorted, in sorted order (Tarjan).
func stronglyConnected(g map[string]map[string]finding.ImportFact) [][]string {
	var nodes []string
	seen := map[string]bool{}
	for from, tos := range g {
		if !seen[from] {
			seen[from] = true
			nodes = append(nodes, from)
		}
		for to := range tos {
			if !seen[to] {
				seen[to] = true
				nodes = append(nodes, to)
			}
		}
	}
	sort.Strings(nodes)
	index, low := map[string]int{}, map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string
	next := 0
	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		var tos []string
		for to := range g[v] {
			tos = append(tos, to)
		}
		sort.Strings(tos)
		for _, w := range tos {
			if _, ok := index[w]; !ok {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 {
				sort.Strings(comp)
				out = append(out, comp)
			}
		}
	}
	for _, n := range nodes {
		if _, ok := index[n]; !ok {
			visit(n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
