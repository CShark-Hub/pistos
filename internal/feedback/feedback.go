// Package feedback owns .pistos/feedback.yaml: the committed, authoritative record of human decisions.
package feedback

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/fsutil"
)

const SchemaVersion = 1

// Reserved tags with policy effects. Every other tag is an ordinary label.
const (
	TagIrrelevant    = "irrelevant"
	TagFalsePositive = "false-positive"
	TagAcceptedRisk  = "accepted-risk"
	TagDeferred      = "deferred"
	TagTestOnly      = "test-only"
)

var suppressing = map[string]bool{TagIrrelevant: true, TagFalsePositive: true, TagAcceptedRisk: true}

func Suppressing(tag string) bool { return suppressing[tag] }

const (
	ScopeIssue   = "issue"
	ScopePattern = "pattern"
)

type File struct {
	Schema    int        `yaml:"schema" json:"schema"`
	Decisions []Decision `yaml:"decisions" json:"decisions"`
}

type Decision struct {
	ID      string   `yaml:"id" json:"id"`
	Tags    []string `yaml:"tags" json:"tags"`
	Reason  string   `yaml:"reason,omitempty" json:"reason,omitempty"`
	Scope   string   `yaml:"scope" json:"scope"`
	Subject string   `yaml:"subject,omitempty" json:"subject,omitempty"`
	Match   *Match   `yaml:"match,omitempty" json:"match,omitempty"`
	Pattern *Pattern `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	Guards  *Guards  `yaml:"guards,omitempty" json:"guards,omitempty"`
	Origin  *Origin  `yaml:"origin,omitempty" json:"origin,omitempty"`
	Version int      `yaml:"version" json:"version"`
}

// Match holds the generated selectors that let a decision survive cache deletion, line shifts and
// verified renames. Users are not expected to author these by hand.
type Match struct {
	Project string   `yaml:"project" json:"project"`
	Path    string   `yaml:"path" json:"path"`
	Symbol  string   `yaml:"symbol,omitempty" json:"symbol,omitempty"`
	Family  string   `yaml:"family" json:"family"`
	Anchor  string   `yaml:"anchor" json:"anchor"`
	Ordinal int      `yaml:"ordinal" json:"ordinal"`
	Aliases []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	// FileBlob is the git blob of the file when tagged; an identical blob elsewhere proves a pure rename.
	FileBlob string `yaml:"file_blob,omitempty" json:"file_blob,omitempty"`
}

type Pattern struct {
	Project string   `yaml:"project,omitempty" json:"project,omitempty"`
	Family  string   `yaml:"family" json:"family"`
	Paths   []string `yaml:"paths,omitempty" json:"paths,omitempty"`
}

// Guards are assumptions that must still hold for the decision to apply.
type Guards struct {
	TestPath bool `yaml:"test_path,omitempty" json:"test_path,omitempty"`
}

type Origin struct {
	Tree   string `yaml:"tree,omitempty" json:"tree,omitempty"`
	Commit string `yaml:"commit,omitempty" json:"commit,omitempty"`
}

var (
	tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	idPattern  = regexp.MustCompile(`^d-[a-z0-9]{6,32}$`)
)

func ValidTag(t string) bool { return tagPattern.MatchString(t) }

func Path(repo string) string { return config.Path(repo, config.FeedbackFile) }

// Load reads and validates feedback.yaml; a missing file is an empty decision set.
func Load(repo string) (*File, error) {
	b, err := os.ReadFile(Path(repo))
	if errors.Is(err, os.ErrNotExist) {
		return &File{Schema: SchemaVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*File, error) {
	f := &File{}
	if len(bytes.TrimSpace(b)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(f); err != nil {
			return nil, fmt.Errorf("feedback.yaml: %w", err)
		}
	} else {
		f.Schema = SchemaVersion
	}
	migrate(f)
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return f, nil
}

// migrate upgrades older layouts in memory; schema 0 (unversioned drafts) had no version counters.
func migrate(f *File) {
	if f.Schema == 0 {
		f.Schema = SchemaVersion
		for i := range f.Decisions {
			if f.Decisions[i].Version == 0 {
				f.Decisions[i].Version = 1
			}
		}
	}
}

func (f *File) Validate() error {
	var errs []error
	add := func(i int, d Decision, format string, a ...any) {
		errs = append(errs, fmt.Errorf("feedback.yaml: decisions[%d] (%s): %s", i, d.ID, fmt.Sprintf(format, a...)))
	}
	if f.Schema != SchemaVersion {
		errs = append(errs, fmt.Errorf("feedback.yaml: schema %d is not supported (expected %d)", f.Schema, SchemaVersion))
	}
	ids := map[string]bool{}
	for i, d := range f.Decisions {
		if !idPattern.MatchString(d.ID) {
			add(i, d, "id must look like d-<hex>")
		}
		if ids[d.ID] {
			add(i, d, "duplicate id")
		}
		ids[d.ID] = true
		if len(d.Tags) == 0 {
			add(i, d, "at least one tag is required")
		}
		for _, t := range d.Tags {
			if !ValidTag(t) {
				add(i, d, "invalid tag %q (lowercase letters, digits and dashes)", t)
			}
		}
		switch d.Scope {
		case ScopeIssue:
			if d.Subject == "" || d.Match == nil || d.Match.Family == "" || d.Match.Path == "" || d.Match.Project == "" || d.Match.Anchor == "" {
				add(i, d, "issue scope requires subject and match.{project,path,family,anchor}")
			}
			if d.Pattern != nil {
				add(i, d, "issue scope cannot also carry a pattern")
			}
		case ScopePattern:
			if d.Pattern == nil || d.Pattern.Family == "" {
				add(i, d, "pattern scope requires pattern.family")
			} else {
				for _, p := range d.Pattern.Paths {
					if _, err := config.CleanRel(strings.ReplaceAll(p, "*", "x")); err != nil {
						add(i, d, "pattern path %q: %v", p, err)
					}
				}
			}
		default:
			add(i, d, "scope must be %q or %q", ScopeIssue, ScopePattern)
		}
		if d.Version < 1 {
			add(i, d, "version must be at least 1")
		}
	}
	return errors.Join(errs...)
}

// Hash identifies the effective decision set; it is part of the published report.
func (f *File) Hash() string {
	b, _ := json.Marshal(f)
	return fsutil.HashBytes(b)[:20]
}

func (f *File) Find(id string) (int, *Decision) {
	for i := range f.Decisions {
		if f.Decisions[i].ID == id {
			return i, &f.Decisions[i]
		}
	}
	return -1, nil
}

func (d *Decision) HasTag(t string) bool {
	for _, x := range d.Tags {
		if x == t {
			return true
		}
	}
	return false
}

func (d *Decision) AddTags(tags ...string) {
	for _, t := range tags {
		if !d.HasTag(t) {
			d.Tags = append(d.Tags, t)
		}
	}
	sort.Strings(d.Tags)
}

func (d *Decision) RemoveTags(pred func(string) bool) {
	var keep []string
	for _, t := range d.Tags {
		if !pred(t) {
			keep = append(keep, t)
		}
	}
	d.Tags = keep
}

func (d *Decision) Suppresses() bool {
	for _, t := range d.Tags {
		if suppressing[t] {
			return true
		}
	}
	return false
}

func (d *Decision) Describe() string {
	switch d.Scope {
	case ScopePattern:
		where := "all paths"
		if len(d.Pattern.Paths) > 0 {
			where = strings.Join(d.Pattern.Paths, ", ")
		}
		proj := "any project"
		if d.Pattern.Project != "" {
			proj = "project " + d.Pattern.Project
		}
		return fmt.Sprintf("pattern: every %s finding in %s (%s)", d.Pattern.Family, where, proj)
	default:
		sym := ""
		if d.Match.Symbol != "" {
			sym = " in " + d.Match.Symbol
		}
		return fmt.Sprintf("issue: %s at %s%s", d.Match.Family, d.Match.Path, sym)
	}
}

func NewID(seed string) string { return "d-" + fsutil.HashStrings("decision", seed)[:10] }

// filePath is resolved relative to the repository so tests can point at temporary repositories.
func lockPath(repo string) string {
	return filepath.Join(repo, config.Dir, config.FeedbackFile+".lock")
}
