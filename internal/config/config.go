// Package config loads and validates .pistos/config.toml and .pistos/lock.json.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	SchemaVersion = 1
	Dir           = ".pistos"
	ConfigFile    = "config.toml"
	LockFile      = "lock.json"
	FeedbackFile  = "feedback.yaml"
)

type Config struct {
	Schema       int                   `toml:"schema"`
	Languages    []string              `toml:"languages"`
	Profile      string                `toml:"profile"`
	Execution    Execution             `toml:"execution"`
	Policy       Policy                `toml:"policy"`
	Projects     []Project             `toml:"projects"`
	AI           AI                    `toml:"ai"`
	Models       map[string]ModelAlias `toml:"models"`
	Metrics      Metrics               `toml:"metrics"`
	Architecture Architecture          `toml:"architecture"`
}

// Metrics thresholds apply to the "metrics" check. Findings are advisory unless Gate is set.
type Metrics struct {
	Gate       bool `toml:"gate"`
	Cyclomatic int  `toml:"cyclomatic"`
	Nesting    int  `toml:"nesting"`
	Params     int  `toml:"params"`
	Statements int  `toml:"statements"`
}

// Architecture rules apply to the "imports" check.
type Architecture struct {
	Gate   bool  `toml:"gate"`
	Cycles *bool `toml:"cycles"`
	// RequireComplete makes unresolved imports an incomplete result instead of a qualified one.
	RequireComplete bool        `toml:"require_complete"`
	Layers          []ArchLayer `toml:"layers"`
	Rules           []ArchRule  `toml:"rules"`
}

type ArchLayer struct {
	Name  string   `toml:"name"`
	Paths []string `toml:"paths"`
}

type ArchRule struct {
	From string   `toml:"from"`
	Deny []string `toml:"deny"`
}

type Execution struct {
	Mode     string   `toml:"mode"`
	Jobs     int      `toml:"jobs"`
	Timeout  Duration `toml:"timeout"`
	MemoryMB int      `toml:"memory_mb"`
}

type Policy struct {
	// BlockOn is the lowest severity of a new deterministic finding that fails the gate.
	BlockOn string `toml:"block_on"`
}

type Project struct {
	ID        string            `toml:"id"`
	Root      string            `toml:"root"`
	Ecosystem string            `toml:"ecosystem"`
	Checks    []string          `toml:"checks"`
	Options   map[string]string `toml:"options"`
	// References lists project IDs whose sources participate in this project's semantic analysis.
	References []string `toml:"references"`
}

type AI struct {
	Enabled      bool     `toml:"enabled"`
	Required     bool     `toml:"required"`
	Aggregation  string   `toml:"aggregation"`
	ReuseResults *bool    `toml:"reuse_results"`
	Discovery    []AIPass `toml:"discovery"`
	Triage       AITriage `toml:"triage"`
	Budget       AIBudget `toml:"budget"`
}

type AIPass struct {
	ID    string `toml:"id"`
	Model string `toml:"model"`
}

type AITriage struct {
	Mode                    string `toml:"mode"`
	Model                   string `toml:"model"`
	KeepSupportedSingletons *bool  `toml:"keep_supported_singletons"`
}

type AIBudget struct {
	MaxCandidates int `toml:"max_candidates"`
	MaxTokens     int `toml:"max_tokens"`
}

type ModelAlias struct {
	Backend   string   `toml:"backend"`
	Endpoint  string   `toml:"endpoint"`
	Model     string   `toml:"model"`
	APIKeyEnv string   `toml:"api_key_env"`
	Timeout   Duration `toml:"timeout"`
	MaxTokens int      `toml:"max_tokens"`
	// Concurrency limits simultaneous requests to this alias's endpoint.
	Concurrency int `toml:"concurrency"`
}

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

var (
	Profiles   = []string{"fast", "balanced", "thorough"}
	Severities = []string{"error", "warning", "info"}
	idPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	envPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

func Path(repo string, name string) string { return filepath.Join(repo, Dir, name) }

// Load reads and validates the repository configuration.
func Load(repo string) (*Config, error) {
	b, err := os.ReadFile(Path(repo, ConfigFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no %s/%s found; run `pistos init` first", Dir, ConfigFile)
		}
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Config, error) {
	var c Config
	md, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&c)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("config: unknown keys: %s%s", strings.Join(keys, ", "), secretHint(keys))
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func secretHint(keys []string) string {
	for _, k := range keys {
		l := strings.ToLower(k)
		if strings.Contains(l, "key") || strings.Contains(l, "token") || strings.Contains(l, "secret") {
			return " (secrets are never stored in config; set api_key_env to the name of an environment variable)"
		}
	}
	return ""
}

func (c *Config) applyDefaults() {
	if c.Profile == "" {
		c.Profile = "balanced"
	}
	if c.Execution.Mode == "" {
		c.Execution.Mode = "native"
	}
	if c.Execution.Jobs == 0 {
		c.Execution.Jobs = 4
	}
	if c.Execution.Timeout.Duration == 0 {
		c.Execution.Timeout.Duration = 10 * time.Minute
	}
	if c.Policy.BlockOn == "" {
		c.Policy.BlockOn = "warning"
	}
	if c.AI.Aggregation == "" {
		c.AI.Aggregation = "union"
	}
	if c.AI.ReuseResults == nil {
		t := true
		c.AI.ReuseResults = &t
	}
	if c.AI.Triage.Mode == "" {
		c.AI.Triage.Mode = "evidence"
	}
	if c.AI.Triage.KeepSupportedSingletons == nil {
		t := true
		c.AI.Triage.KeepSupportedSingletons = &t
	}
	setDefault := func(v *int, d int) {
		if *v == 0 {
			*v = d
		}
	}
	setDefault(&c.Metrics.Cyclomatic, 10)
	setDefault(&c.Metrics.Nesting, 4)
	setDefault(&c.Metrics.Params, 5)
	setDefault(&c.Metrics.Statements, 50)
	if c.Architecture.Cycles == nil {
		t := true
		c.Architecture.Cycles = &t
	}
	for i := range c.Projects {
		if c.Projects[i].Root == "" {
			c.Projects[i].Root = "."
		}
	}
}

func (c *Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if c.Schema != SchemaVersion {
		add("schema = %d is not supported (this pistos supports schema %d)", c.Schema, SchemaVersion)
	}
	for _, l := range c.Languages {
		if _, err := NormalizeLanguage(l); err != nil {
			add("languages: %v", err)
		}
	}
	if !contains(Profiles, c.Profile) {
		add("profile %q must be one of %s", c.Profile, strings.Join(Profiles, ", "))
	}
	if c.Execution.Mode != "native" && c.Execution.Mode != "docker" {
		add("execution.mode %q must be native or docker", c.Execution.Mode)
	}
	if c.Execution.Jobs < 1 || c.Execution.Jobs > 256 {
		add("execution.jobs must be between 1 and 256")
	}
	if !contains(Severities, c.Policy.BlockOn) && c.Policy.BlockOn != "none" {
		add("policy.block_on %q must be one of error, warning, info, none", c.Policy.BlockOn)
	}

	ids := map[string]bool{}
	for i, p := range c.Projects {
		where := fmt.Sprintf("projects[%d]", i)
		if !idPattern.MatchString(p.ID) {
			add("%s.id %q must match %s", where, p.ID, idPattern)
		}
		if ids[p.ID] {
			add("%s.id %q is duplicated", where, p.ID)
		}
		ids[p.ID] = true
		if _, err := CleanRel(p.Root); err != nil {
			add("%s.root: %v", where, err)
		}
		eco, err := NormalizeLanguage(p.Ecosystem)
		if err != nil {
			add("%s.ecosystem: %v", where, err)
		} else if len(c.Languages) > 0 && !contains(c.Languages, eco) && !containsNormalized(c.Languages, eco) {
			add("%s.ecosystem %q is not listed in languages", where, eco)
		}
	}
	for i, p := range c.Projects {
		for _, r := range p.References {
			if !ids[r] {
				add("projects[%d].references: unknown project %q", i, r)
			}
		}
	}

	if c.AI.Aggregation != "union" {
		add("ai.aggregation %q is not supported; only \"union\" is", c.AI.Aggregation)
	}
	if c.AI.Triage.Mode != "evidence" && c.AI.Triage.Mode != "model" {
		add("ai.triage.mode %q must be evidence or model", c.AI.Triage.Mode)
	}
	passIDs := map[string]bool{}
	for i, p := range c.AI.Discovery {
		if !idPattern.MatchString(p.ID) || passIDs[p.ID] {
			add("ai.discovery[%d].id %q must be unique and match %s", i, p.ID, idPattern)
		}
		passIDs[p.ID] = true
		if _, ok := c.Models[p.Model]; !ok {
			add("ai.discovery[%d].model %q is not a defined [models.*] alias", i, p.Model)
		}
	}
	if c.AI.Triage.Mode == "model" {
		if _, ok := c.Models[c.AI.Triage.Model]; !ok {
			add("ai.triage.model %q is not a defined [models.*] alias", c.AI.Triage.Model)
		}
	}
	if c.AI.Enabled && len(c.AI.Discovery) == 0 {
		add("ai.enabled requires at least one [[ai.discovery]] pass")
	}
	for _, name := range sortedKeys(c.Models) {
		m := c.Models[name]
		where := "models." + name
		if m.Backend != "openai-compatible" {
			add("%s.backend %q is not supported (supported: openai-compatible)", where, m.Backend)
		}
		if m.Model == "" {
			add("%s.model is required", where)
		}
		if err := ValidateEndpoint(m.Endpoint); err != nil {
			add("%s.endpoint: %v", where, err)
		}
		if m.APIKeyEnv != "" && !envPattern.MatchString(m.APIKeyEnv) {
			add("%s.api_key_env must name an environment variable, not hold a secret", where)
		}
	}
	for name, v := range map[string]int{"cyclomatic": c.Metrics.Cyclomatic, "nesting": c.Metrics.Nesting, "params": c.Metrics.Params, "statements": c.Metrics.Statements} {
		if v < 1 {
			add("metrics.%s must be a positive threshold", name)
		}
	}
	layers := map[string]bool{}
	for i, l := range c.Architecture.Layers {
		if !idPattern.MatchString(l.Name) || layers[l.Name] {
			add("architecture.layers[%d].name %q must be unique and match %s", i, l.Name, idPattern)
		}
		layers[l.Name] = true
		if len(l.Paths) == 0 {
			add("architecture.layers[%d] needs at least one path glob", i)
		}
		for _, p := range l.Paths {
			if _, err := CleanRel(strings.ReplaceAll(p, "*", "x")); err != nil {
				add("architecture.layers[%d].paths: %v", i, err)
			}
		}
	}
	for i, r := range c.Architecture.Rules {
		for _, n := range append([]string{r.From}, r.Deny...) {
			if !layers[n] {
				add("architecture.rules[%d] references undefined layer %q", i, n)
			}
		}
	}
	return errors.Join(errs...)
}

// ValidateEndpoint requires HTTPS except for explicitly local endpoints.
func ValidateEndpoint(raw string) error {
	switch {
	case raw == "":
		return errors.New("is required")
	case strings.HasPrefix(raw, "https://"):
		return nil
	case strings.HasPrefix(raw, "http://"):
		host := strings.TrimPrefix(raw, "http://")
		if i := strings.IndexAny(host, "/"); i >= 0 {
			host = host[:i]
		}
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h
		}
		if host == "localhost" || host == "127.0.0.1" || host == "[::1]" {
			return nil
		}
		return fmt.Errorf("%q must use https unless it is a local endpoint", raw)
	default:
		return fmt.Errorf("%q must be an http(s) URL", raw)
	}
}

// CleanRel validates a repository-relative path and returns it with forward slashes.
func CleanRel(p string) (string, error) {
	if p == "" {
		return ".", nil
	}
	s := path.Clean(filepath.ToSlash(p))
	if path.IsAbs(s) || filepath.IsAbs(p) || s == ".." || strings.HasPrefix(s, "../") || filepath.VolumeName(p) != "" {
		return "", fmt.Errorf("%q must be a path inside the repository", p)
	}
	return s, nil
}

func (c *Config) ActiveLanguages() []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range c.Languages {
		n, _ := NormalizeLanguage(l)
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func containsNormalized(list []string, s string) bool {
	for _, v := range list {
		if n, _ := NormalizeLanguage(v); n == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
