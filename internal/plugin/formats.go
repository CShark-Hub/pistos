package plugin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Output is the raw result of one analyzer invocation.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	// Artifacts are the files a tool wrote into its declared artifact directory, in name order.
	Artifacts [][]byte
}

// Parsed is normalized analyzer output. Incomplete explains why the covered scope is partial.
type Parsed struct {
	Diagnostics []Diagnostic
	Limitations []string
	Incomplete  string
}

type parser func(Output) (Parsed, error)

var parsers = map[string]parser{
	"go-vet":           parseGoVet,
	"staticcheck-json": parseStaticcheck,
}

func Parse(format string, out Output) (Parsed, error) {
	p, ok := parsers[format]
	if !ok {
		return Parsed{}, fmt.Errorf("unsupported output format %q", format)
	}
	return p(out)
}

var positionLine = regexp.MustCompile(`^(.+?\.[A-Za-z0-9]+):(\d+)(?::(\d+))?: (.*)$`)

func parsePosition(s string) (Diagnostic, bool) {
	m := positionLine.FindStringSubmatch(s)
	if m == nil {
		return Diagnostic{}, false
	}
	line, _ := strconv.Atoi(m[2])
	col, _ := strconv.Atoi(m[3])
	return Diagnostic{Path: m[1], Line: line, Column: col, Message: m[4]}, true
}

type vetDiag struct {
	Posn    string `json:"posn"`
	Message string `json:"message"`
}

// parseGoVet reads `go vet -json` output, which interleaves "# pkg" headers, "vet: pos: msg" type
// errors, package load errors and one JSON object per package. Go 1.26 prints the JSON on stdout and
// Go 1.27 on stderr, so both streams are scanned.
func parseGoVet(out Output) (Parsed, error) {
	var p Parsed
	var other []string
	lines := strings.Split(string(out.Stdout)+"\n"+string(out.Stderr), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case line == "{":
			var block bytes.Buffer
			for ; i < len(lines); i++ {
				block.WriteString(lines[i])
				block.WriteByte('\n')
				if strings.TrimRight(lines[i], "\r") == "}" {
					break
				}
			}
			var pkgs map[string]map[string]json.RawMessage
			if err := json.Unmarshal(block.Bytes(), &pkgs); err != nil {
				return p, fmt.Errorf("go vet JSON: %w", err)
			}
			for pkg, analyzers := range pkgs {
				for analyzer, raw := range analyzers {
					var diags []vetDiag
					if err := json.Unmarshal(raw, &diags); err != nil {
						var e struct {
							Error string `json:"error"`
						}
						_ = json.Unmarshal(raw, &e)
						p.Incomplete = fmt.Sprintf("analyzer %s failed on %s: %s", analyzer, pkg, e.Error)
						continue
					}
					for _, d := range diags {
						pos, ok := parsePosition(d.Posn + ": " + d.Message)
						if !ok {
							return p, fmt.Errorf("go vet: unparsable position %q", d.Posn)
						}
						pos.Rule, pos.Severity = analyzer, "warning"
						p.Diagnostics = append(p.Diagnostics, pos)
					}
				}
			}
		case strings.HasPrefix(line, "vet: "):
			d, ok := parsePosition(strings.TrimPrefix(line, "vet: "))
			if !ok {
				other = append(other, line)
				continue
			}
			d.Rule, d.Severity = "typecheck", "error"
			p.Diagnostics = append(p.Diagnostics, d)
		default:
			if d, ok := parsePosition(line); ok {
				d.Rule, d.Severity = "load", "error"
				p.Diagnostics = append(p.Diagnostics, d)
				p.Incomplete = "go vet could not load every package; analysis stopped at load errors"
				continue
			}
			other = append(other, line)
		}
	}
	if out.ExitCode != 0 && len(p.Diagnostics) == 0 {
		return p, fmt.Errorf("go vet failed: %s", tail(other))
	}
	if len(other) > 0 && out.ExitCode != 0 && p.Incomplete == "" {
		p.Incomplete = "go vet reported errors without positions: " + tail(other)
	}
	return p, nil
}

type staticcheckDiag struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Location struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"location"`
	End struct {
		Line   int `json:"line"`
		Column int `json:"column"`
	} `json:"end"`
	Message string `json:"message"`
}

func parseStaticcheck(out Output) (Parsed, error) {
	var p Parsed
	sc := bufio.NewScanner(bytes.NewReader(out.Stdout))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var d staticcheckDiag
		if err := json.Unmarshal(line, &d); err != nil {
			return p, fmt.Errorf("staticcheck: malformed output line %.200q", line)
		}
		if d.Location.File == "" {
			if strings.Contains(d.Message, "export data version") || strings.Contains(d.Message, "internal error in importing") {
				p.Incomplete = "staticcheck cannot read this Go toolchain's export data (incompatible staticcheck/Go versions)"
				continue
			}
			found := false
			for _, l := range strings.Split(d.Message, "\n") {
				if pd, ok := parsePosition(strings.TrimSpace(l)); ok {
					pd.Rule, pd.Severity = d.Code, "error"
					p.Diagnostics = append(p.Diagnostics, pd)
					found = true
				}
			}
			if !found {
				p.Incomplete = "staticcheck: " + firstLine(d.Message)
			}
			continue
		}
		p.Diagnostics = append(p.Diagnostics, Diagnostic{
			Path: d.Location.File, Line: d.Location.Line, Column: d.Location.Column,
			EndLine: d.End.Line, EndColumn: d.End.Column,
			Rule: d.Code, Severity: mapSeverity(d.Severity), Message: d.Message,
		})
	}
	if err := sc.Err(); err != nil {
		return p, err
	}
	if out.ExitCode != 0 && len(p.Diagnostics) == 0 && p.Incomplete == "" {
		return p, fmt.Errorf("staticcheck failed: %s", tail(strings.Split(string(out.Stderr), "\n")))
	}
	return p, nil
}

func mapSeverity(s string) string {
	switch strings.ToLower(s) {
	case "error", "fatal":
		return "error"
	case "warning", "warn":
		return "warning"
	default:
		return "info"
	}
}

func tail(lines []string) string {
	var keep []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	if len(keep) > 5 {
		keep = keep[len(keep)-5:]
	}
	if len(keep) == 0 {
		return "(no output)"
	}
	return strings.Join(keep, "; ")
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
