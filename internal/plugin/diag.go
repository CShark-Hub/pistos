// Package plugin loads pack manifests, resolves locked tools and runs declarative checks and workers.
package plugin

// Diagnostic is a tool finding before identity assignment; paths may still be tool-relative.
type Diagnostic struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
	Rule      string `json:"rule"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
}
