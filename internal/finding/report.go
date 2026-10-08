package finding

import (
	"bytes"
	"encoding/json"
	"sort"
)

type Project struct {
	ID         string            `json:"id"`
	Root       string            `json:"root"`
	Ecosystem  string            `json:"ecosystem"`
	Checks     []string          `json:"checks"`
	Options    map[string]string `json:"options,omitempty"`
	References []string          `json:"references,omitempty"`
}

// InputManifest identifies everything an analysis result depends on.
type InputManifest struct {
	Tree        string            `json:"tree"`
	ProjectHash string            `json:"project_hash"`
	SharedHash  string            `json:"shared_hash,omitempty"`
	EnvHash     string            `json:"env_hash,omitempty"`
	Tool        string            `json:"tool"`
	ToolVersion string            `json:"tool_version"`
	ToolSHA256  string            `json:"tool_sha256,omitempty"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env,omitempty"`
	Pack        string            `json:"pack"`
	PackVersion string            `json:"pack_version"`
}

// Task is one fixed unit of the deterministic check plan.
type Task struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	Capability string `json:"capability"`
	Side       string `json:"side"`
	Scope      string `json:"scope"`
	Weight     int    `json:"weight"`
	Required   bool   `json:"required"`
}

type CheckPlan struct {
	Profile string `json:"profile"`
	Tasks   []Task `json:"tasks"`
}

type CapabilityResult struct {
	Capability  string    `json:"capability"`
	Project     string    `json:"project"`
	Side        string    `json:"side"`
	Status      Status    `json:"status"`
	Required    bool      `json:"required"`
	Scope       string    `json:"scope"`
	Findings    []Finding `json:"findings,omitempty"`
	Limitations []string  `json:"limitations,omitempty"`
	Error       string    `json:"error,omitempty"`
	InputKey    string    `json:"input_key,omitempty"`
	Tool        string    `json:"tool,omitempty"`
	ToolVersion string    `json:"tool_version,omitempty"`
}

// Disposition explains how feedback affected a finding.
type Disposition struct {
	State    string   `json:"state"`
	Decision string   `json:"decision,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Note     string   `json:"note,omitempty"`
}

const (
	DispActive     = "active"
	DispSuppressed = "suppressed"
	DispDeferred   = "deferred"
	DispRevalidate = "needs-revalidation"
)

// Issue is a finding with its relationship to the baseline and to human decisions.
type Issue struct {
	Finding
	// Change is new, existing or unknown relative to the baseline.
	Change      string      `json:"change"`
	Disposition Disposition `json:"disposition"`
	Blocking    bool        `json:"blocking"`
}

// Candidate is an unverified AI discovery before triage.
type Candidate struct {
	ID              string     `json:"id"`
	Pass            string     `json:"pass"`
	ModelAlias      string     `json:"model_alias"`
	Family          string     `json:"family"`
	Anchors         []Location `json:"anchors"`
	Hypothesis      string     `json:"hypothesis"`
	Assumptions     []string   `json:"assumptions"`
	Impact          string     `json:"impact"`
	FailingScenario string     `json:"failing_scenario"`
	EvidenceRefs    []string   `json:"evidence_refs,omitempty"`
	Cluster         string     `json:"cluster,omitempty"`
	Outcome         string     `json:"outcome,omitempty"`
}

type Scope struct {
	Mode         string   `json:"mode"`
	BaseRef      string   `json:"base_ref,omitempty"`
	HeadRef      string   `json:"head_ref,omitempty"`
	BaseCommit   string   `json:"base_commit,omitempty"`
	BaseTree     string   `json:"base_tree,omitempty"`
	HeadTree     string   `json:"head_tree"`
	ChangedFiles []string `json:"changed_files"`
	Projects     []string `json:"projects"`
	Empty        bool     `json:"empty"`
}

type Verdict struct {
	Outcome  string `json:"outcome"`
	ExitCode int    `json:"exit_code"`
	Blocking int    `json:"blocking"`
	Reason   string `json:"reason"`
}

type Coverage struct {
	Profile      string             `json:"profile"`
	Capabilities []CapabilityResult `json:"capabilities"`
	AI           string             `json:"ai"`
}

// Report is the canonical, byte-comparable review result. Volatile data lives in Envelope.
type Report struct {
	Schema   int      `json:"schema"`
	Scope    Scope    `json:"scope"`
	Verdict  Verdict  `json:"verdict"`
	Coverage Coverage `json:"coverage"`
	Issues   []Issue  `json:"issues"`
	Feedback string   `json:"feedback_hash"`
}

type Envelope struct {
	Canonical *Report        `json:"canonical"`
	Numbers   map[string]int `json:"numbers"`
	Telemetry Telemetry      `json:"telemetry"`
}

type Telemetry struct {
	StartedAt  string          `json:"started_at"`
	DurationMS int64           `json:"duration_ms"`
	Jobs       int             `json:"jobs"`
	Tasks      []TaskTelemetry `json:"tasks,omitempty"`
}

type TaskTelemetry struct {
	Task       string `json:"task"`
	DurationMS int64  `json:"duration_ms"`
	CacheHit   bool   `json:"cache_hit"`
}

// Normalize applies canonical ordering to every collection in the report.
func (r *Report) Normalize() {
	sort.Strings(r.Scope.ChangedFiles)
	sort.Strings(r.Scope.Projects)
	caps := r.Coverage.Capabilities
	sort.SliceStable(caps, func(i, j int) bool {
		a, b := caps[i], caps[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Capability != b.Capability {
			return a.Capability < b.Capability
		}
		return a.Side < b.Side
	})
	for i := range caps {
		caps[i].Findings = nil
		sort.Strings(caps[i].Limitations)
	}
	sort.SliceStable(r.Issues, func(i, j int) bool {
		a, b := r.Issues[i], r.Issues[j]
		if a.Blocking != b.Blocking {
			return a.Blocking
		}
		return CanonicalLess(a.Finding, b.Finding)
	})
}

// CanonicalJSON encodes deterministically: stable ordering, no HTML escaping, trailing newline.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
