package report

import (
	"encoding/json"
	"io"
	"sort"
	"strings"

	"example.com/pistos/internal/finding"
)

const sarifSchema = "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json"

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool               sarifTool                `json:"tool"`
	OriginalURIBaseIDs map[string]sarifArtifact `json:"originalUriBaseIds"`
	Results            []sarifResult            `json:"results"`
	Invocations        []sarifInvocation        `json:"invocations"`
	Properties         map[string]any           `json:"properties,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifMessage `json:"shortDescription"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifArtifact struct {
	URI         string        `json:"uri,omitempty"`
	Description *sarifMessage `json:"description,omitempty"`
}

type sarifResult struct {
	RuleID              string             `json:"ruleId"`
	RuleIndex           int                `json:"ruleIndex"`
	Level               string             `json:"level"`
	Message             sarifMessage       `json:"message"`
	Locations           []sarifLocation    `json:"locations"`
	PartialFingerprints map[string]string  `json:"partialFingerprints"`
	Suppressions        []sarifSuppression `json:"suppressions,omitempty"`
	BaselineState       string             `json:"baselineState,omitempty"`
	Properties          map[string]any     `json:"properties"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion          `json:"region,omitempty"`
}

type sarifArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

type sarifSuppression struct {
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Justification string `json:"justification,omitempty"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool                `json:"executionSuccessful"`
	ExitCode            int                 `json:"exitCode"`
	Notifications       []sarifNotification `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotification struct {
	Level   string       `json:"level"`
	Message sarifMessage `json:"message"`
}

// SARIF writes the report as SARIF 2.1.0. Paths are repository-relative against %SRCROOT%;
// suppressed issues are included with an external suppression so they stay auditable.
func SARIF(w io.Writer, env *finding.Envelope, version string) error {
	r := env.Canonical
	families := map[string]string{}
	for _, is := range r.Issues {
		if _, ok := families[is.Family]; !ok {
			families[is.Family] = is.Message
		}
	}
	ids := make([]string, 0, len(families))
	for id := range families {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	index := map[string]int{}
	var rules []sarifRule
	for i, id := range ids {
		index[id] = i
		rules = append(rules, sarifRule{ID: id, ShortDescription: sarifMessage{Text: id}})
	}

	run := sarifRun{
		Tool:               sarifTool{Driver: sarifDriver{Name: "pistos", Version: version, Rules: rules}},
		OriginalURIBaseIDs: map[string]sarifArtifact{"SRCROOT": {Description: &sarifMessage{Text: "repository root"}}},
		Results:            []sarifResult{},
		Properties:         map[string]any{"verdict": r.Verdict, "headTree": r.Scope.HeadTree, "baseTree": r.Scope.BaseTree},
	}
	inv := sarifInvocation{ExecutionSuccessful: r.Verdict.Outcome != "incomplete", ExitCode: r.Verdict.ExitCode}
	for _, c := range r.Coverage.Capabilities {
		if c.Required && !c.Status.Completed() {
			inv.Notifications = append(inv.Notifications, sarifNotification{Level: "error", Message: sarifMessage{Text: c.Project + "/" + c.Capability + ": " + string(c.Status) + " " + c.Error}})
		}
	}
	run.Invocations = []sarifInvocation{inv}

	for _, is := range r.Issues {
		res := sarifResult{
			RuleID: is.Family, RuleIndex: index[is.Family], Level: sarifLevelFor(is),
			Message:             sarifMessage{Text: is.Message},
			PartialFingerprints: map[string]string{"pistos/v1": is.Key},
			Properties: map[string]any{
				"lane": is.Lane, "project": is.Project, "change": is.Change, "blocking": is.Blocking,
				"advisory": is.Advisory, "number": env.Numbers[is.Key], "sources": is.Sources,
			},
		}
		if is.AI != nil {
			res.Properties["ai"] = is.AI
		}
		// Results never carry snippets; consumers that add their own must withhold sensitive lines.
		if is.Sensitive {
			res.Properties["sensitive"] = true
		}
		switch is.Change {
		case "new", "worsened", "unknown":
			res.BaselineState = "new"
		case "existing":
			res.BaselineState = "unchanged"
		}
		loc := sarifPhysical{ArtifactLocation: sarifArtifactLocation{URI: uriPath(is.Location.Path), URIBaseID: "SRCROOT"}}
		if is.Location.Line > 0 {
			loc.Region = &sarifRegion{StartLine: is.Location.Line, StartColumn: is.Location.Column}
			if is.Location.EndLine >= is.Location.Line {
				loc.Region.EndLine = is.Location.EndLine
				if is.Location.EndLine > is.Location.Line || is.Location.EndColumn > is.Location.Column {
					loc.Region.EndColumn = is.Location.EndColumn
				}
			}
		}
		res.Locations = []sarifLocation{{PhysicalLocation: loc}}
		if is.Disposition.State == finding.DispSuppressed {
			res.Suppressions = []sarifSuppression{{Kind: "external", Status: "accepted", Justification: strings.Join(is.Disposition.Tags, ",") + " (" + is.Disposition.Decision + ")"}}
		}
		run.Results = append(run.Results, res)
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(sarifLog{Schema: sarifSchema, Version: "2.1.0", Runs: []sarifRun{run}})
}

func sarifLevelFor(is finding.Issue) string {
	if is.Advisory {
		return "note"
	}
	switch is.Severity {
	case finding.SevError:
		return "error"
	case finding.SevWarning:
		return "warning"
	}
	return "note"
}

// uriPath percent-encodes characters that are not valid in a relative URI reference.
func uriPath(p string) string {
	var b strings.Builder
	for _, c := range []byte(p) {
		if c == '/' || c == '-' || c == '_' || c == '.' || c == '~' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
			continue
		}
		b.WriteString("%" + strings.ToUpper(hex2(c)))
	}
	return b.String()
}

func hex2(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&15]})
}
