package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	parsers["ruff-json"] = parseRuff
	parsers["pyright-json"] = parsePyright
	parsers["tsc-text"] = parseTsc
	parsers["eslint-json"] = parseESLint
	parsers["oxlint-json"] = parseOxlint
	parsers["sarif-artifacts"] = parseSARIFArtifacts
}

type ruffDiag struct {
	Code     *string `json:"code"`
	Name     string  `json:"name"`
	Message  string  `json:"message"`
	Filename string  `json:"filename"`
	Severity string  `json:"severity"`
	Location struct {
		Row    int `json:"row"`
		Column int `json:"column"`
	} `json:"location"`
	EndLocation struct {
		Row    int `json:"row"`
		Column int `json:"column"`
	} `json:"end_location"`
}

// parseRuff reads `ruff check --output-format json`. Ruff prints "[]" even when it could not read
// the files (e.g. a sandboxed snap build), so any error on stderr makes the result incomplete.
func parseRuff(out Output) (Parsed, error) {
	var p Parsed
	var diags []ruffDiag
	if err := json.Unmarshal(bytes.TrimSpace(out.Stdout), &diags); err != nil {
		return p, fmt.Errorf("ruff: malformed JSON output: %v; stderr: %s", err, tail(strings.Split(string(out.Stderr), "\n")))
	}
	for _, d := range diags {
		rule := d.Name
		if d.Code != nil && *d.Code != "" {
			rule = *d.Code
		}
		sev := mapSeverity(d.Severity)
		if d.Severity == "" {
			sev = "warning"
		}
		if rule == "invalid-syntax" || rule == "" {
			rule, sev = "invalid-syntax", "error"
		}
		p.Diagnostics = append(p.Diagnostics, Diagnostic{
			Path: d.Filename, Line: d.Location.Row, Column: d.Location.Column,
			EndLine: d.EndLocation.Row, EndColumn: d.EndLocation.Column,
			Rule: rule, Severity: sev, Message: d.Message,
		})
	}
	for _, l := range strings.Split(string(out.Stderr), "\n") {
		if strings.Contains(l, "Encountered error") || strings.HasPrefix(strings.TrimSpace(l), "error:") {
			p.Incomplete = "ruff could not analyze every file: " + strings.TrimSpace(l)
			break
		}
	}
	return p, nil
}

type pyrightReport struct {
	Version            string `json:"version"`
	GeneralDiagnostics []struct {
		File     string `json:"file"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Rule     string `json:"rule"`
		Range    *struct {
			Start struct{ Line, Character int } `json:"start"`
			End   struct{ Line, Character int } `json:"end"`
		} `json:"range"`
	} `json:"generalDiagnostics"`
	Summary struct {
		FilesAnalyzed int `json:"filesAnalyzed"`
		ErrorCount    int `json:"errorCount"`
	} `json:"summary"`
}

// parsePyright reads `pyright --outputjson` (also emitted by basedpyright). Ranges are zero-based.
func parsePyright(out Output) (Parsed, error) {
	var p Parsed
	var r pyrightReport
	if err := json.Unmarshal(bytes.TrimSpace(out.Stdout), &r); err != nil {
		return p, fmt.Errorf("pyright: malformed JSON output: %v; stderr: %s", err, tail(strings.Split(string(out.Stderr), "\n")))
	}
	for _, d := range r.GeneralDiagnostics {
		if d.Range == nil {
			p.Incomplete = "pyright reported a problem without a location: " + firstLine(d.Message)
			continue
		}
		rule := d.Rule
		if rule == "" {
			rule = "general"
		}
		p.Diagnostics = append(p.Diagnostics, Diagnostic{
			Path: d.File, Line: d.Range.Start.Line + 1, Column: d.Range.Start.Character + 1,
			EndLine: d.Range.End.Line + 1, EndColumn: d.Range.End.Character + 1,
			Rule: rule, Severity: mapSeverity(d.Severity), Message: firstLine(d.Message),
		})
	}
	if r.Summary.FilesAnalyzed == 0 {
		p.Incomplete = "pyright analyzed no files; check include/exclude settings"
	}
	return p, nil
}

var tscLine = regexp.MustCompile(`^(.+)\((\d+),(\d+)\): (error|warning|message) (TS\d+): (.*)$`)
var tscGlobal = regexp.MustCompile(`^(error|warning) (TS\d+): (.*)$`)

// parseTsc reads `tsc --pretty false` text, the only stable machine-readable form tsc offers.
// Continuation lines (indented) extend the previous message.
func parseTsc(out Output) (Parsed, error) {
	var p Parsed
	var other []string
	text := string(out.Stdout) + "\n" + string(out.Stderr)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := tscLine.FindStringSubmatch(line); m != nil {
			l, _ := strconv.Atoi(m[2])
			c, _ := strconv.Atoi(m[3])
			p.Diagnostics = append(p.Diagnostics, Diagnostic{Path: m[1], Line: l, Column: c, Rule: m[5], Severity: mapSeverity(m[4]), Message: m[6]})
			continue
		}
		if m := tscGlobal.FindStringSubmatch(line); m != nil {
			p.Incomplete = fmt.Sprintf("tsc reported a project-level %s: %s %s", m[1], m[2], m[3])
			continue
		}
		if strings.HasPrefix(line, "  ") && len(p.Diagnostics) > 0 {
			continue
		}
		if strings.TrimSpace(line) != "" {
			other = append(other, line)
		}
	}
	if out.ExitCode != 0 && len(p.Diagnostics) == 0 && p.Incomplete == "" {
		return p, fmt.Errorf("tsc failed: %s", tail(other))
	}
	return p, nil
}

type eslintFile struct {
	FilePath string `json:"filePath"`
	Messages []struct {
		RuleID    *string `json:"ruleId"`
		Severity  int     `json:"severity"`
		Message   string  `json:"message"`
		Line      int     `json:"line"`
		Column    int     `json:"column"`
		EndLine   int     `json:"endLine"`
		EndColumn int     `json:"endColumn"`
		Fatal     bool    `json:"fatal"`
	} `json:"messages"`
}

func parseESLint(out Output) (Parsed, error) {
	var p Parsed
	var files []eslintFile
	if err := json.Unmarshal(bytes.TrimSpace(out.Stdout), &files); err != nil {
		return p, fmt.Errorf("eslint: malformed JSON output: %v; stderr: %s", err, tail(strings.Split(string(out.Stderr), "\n")))
	}
	for _, f := range files {
		for _, m := range f.Messages {
			rule, sev := "", "warning"
			if m.RuleID != nil {
				rule = *m.RuleID
			}
			if m.Severity == 2 {
				sev = "error"
			}
			if m.Fatal || rule == "" {
				rule, sev = "parse-error", "error"
			}
			p.Diagnostics = append(p.Diagnostics, Diagnostic{
				Path: f.FilePath, Line: m.Line, Column: m.Column, EndLine: m.EndLine, EndColumn: m.EndColumn,
				Rule: rule, Severity: sev, Message: m.Message,
			})
		}
	}
	return p, nil
}

type oxlintReport struct {
	Diagnostics []struct {
		Message  string `json:"message"`
		Code     string `json:"code"`
		Severity string `json:"severity"`
		Filename string `json:"filename"`
		Labels   []struct {
			Span struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"span"`
		} `json:"labels"`
	} `json:"diagnostics"`
	NumberOfFiles *int `json:"number_of_files"`
}

var oxlintCode = regexp.MustCompile(`^([\w@/-]+)\(([\w@/-]+)\)$`)

func parseOxlint(out Output) (Parsed, error) {
	var p Parsed
	var r oxlintReport
	if err := json.Unmarshal(bytes.TrimSpace(out.Stdout), &r); err != nil {
		return p, fmt.Errorf("oxlint: malformed JSON output: %v; stderr: %s", err, tail(strings.Split(string(out.Stderr), "\n")))
	}
	if r.NumberOfFiles == nil {
		return p, fmt.Errorf("oxlint: output lacks number_of_files; unsupported format version")
	}
	for _, d := range r.Diagnostics {
		rule := d.Code
		if m := oxlintCode.FindStringSubmatch(d.Code); m != nil {
			rule = m[1] + "/" + m[2]
		}
		line, col := 0, 0
		if len(d.Labels) > 0 {
			line, col = d.Labels[0].Span.Line, d.Labels[0].Span.Column
		}
		p.Diagnostics = append(p.Diagnostics, Diagnostic{Path: d.Filename, Line: line, Column: col, Rule: rule, Severity: mapSeverity(d.Severity), Message: d.Message})
	}
	return p, nil
}

type sarifLog struct {
	Version string `json:"version"`
	Runs    []struct {
		Tool struct {
			Driver struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region struct {
						StartLine   int `json:"startLine"`
						StartColumn int `json:"startColumn"`
						EndLine     int `json:"endLine"`
						EndColumn   int `json:"endColumn"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
			Suppressions []json.RawMessage `json:"suppressions"`
		} `json:"results"`
	} `json:"runs"`
}

// parseSARIFArtifacts reads SARIF 2.1 logs, one per compilation (project and target framework).
// In-source suppressions are honoured; results without a location cannot be anchored and are counted.
func parseSARIFArtifacts(out Output) (Parsed, error) {
	var p Parsed
	if len(out.Artifacts) == 0 {
		if out.ExitCode != 0 {
			return p, fmt.Errorf("build failed before producing diagnostics: %s", tail(errorLines(out)))
		}
		p.Incomplete = "the build produced no compiler diagnostics log; analysis did not run"
		return p, nil
	}
	unanchored := 0
	for _, a := range out.Artifacts {
		var log sarifLog
		if err := json.Unmarshal(a, &log); err != nil {
			return p, fmt.Errorf("SARIF: %w", err)
		}
		if !strings.HasPrefix(log.Version, "2.1") {
			return p, fmt.Errorf("SARIF version %q is not supported", log.Version)
		}
		for _, run := range log.Runs {
			for _, r := range run.Results {
				if len(r.Suppressions) > 0 {
					continue
				}
				if len(r.Locations) == 0 || r.Locations[0].PhysicalLocation.ArtifactLocation.URI == "" {
					unanchored++
					continue
				}
				loc := r.Locations[0].PhysicalLocation
				p.Diagnostics = append(p.Diagnostics, Diagnostic{
					Path: uriToPath(loc.ArtifactLocation.URI), Line: loc.Region.StartLine, Column: loc.Region.StartColumn,
					EndLine: loc.Region.EndLine, EndColumn: loc.Region.EndColumn,
					Rule: r.RuleID, Severity: sarifLevel(r.Level), Message: r.Message.Text,
				})
			}
		}
	}
	if unanchored > 0 {
		p.Limitations = append(p.Limitations, fmt.Sprintf("%d project-level diagnostics without a source location were not reported", unanchored))
	}
	if out.ExitCode != 0 {
		hasError := false
		for _, d := range p.Diagnostics {
			hasError = hasError || d.Severity == "error"
		}
		if !hasError {
			p.Incomplete = "build failed without compiler errors: " + tail(errorLines(out))
		}
	}
	return p, nil
}

func errorLines(out Output) []string {
	var lines []string
	for _, l := range strings.Split(string(out.Stdout)+"\n"+string(out.Stderr), "\n") {
		if strings.Contains(l, "error") {
			lines = append(lines, l)
		}
	}
	return lines
}

func sarifLevel(l string) string {
	switch l {
	case "error":
		return "error"
	case "warning", "":
		return "warning"
	default:
		return "info"
	}
}

func uriToPath(uri string) string {
	if u, err := url.Parse(uri); err == nil && u.Scheme == "file" {
		p := u.Path
		if len(p) > 2 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		return filepath.FromSlash(p)
	}
	return filepath.FromSlash(uri)
}
