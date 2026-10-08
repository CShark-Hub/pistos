package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/fsutil"
	"example.com/pistos/internal/runner"
)

var (
	ErrNotFound  = errors.New("tool not found")
	ErrNotLocked = errors.New("tool is not locked")
	ErrChanged   = errors.New("tool changed since it was locked")
)

type ResolvedTool struct {
	Pack     string `json:"pack"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Version  string `json:"version"`
	Identity string `json:"identity"`
	Source   string `json:"source"`
	LockKey  string `json:"lock_key"`
}

func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// CacheRoot is the machine-wide cache for materialized trees and managed tools.
func CacheRoot() (string, error) {
	if d := os.Getenv("PISTOS_CACHE_DIR"); d != "" {
		return d, nil
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "pistos"), nil
}

func ManagedBin(pack, tool, version string) (string, error) {
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "tools", pack, tool, version, "bin"), nil
}

// Locate finds a tool executable following the manifest's lookup order without running it.
func Locate(m *Manifest, name, repo, projectDir string) (path, source string, err error) {
	t, ok := m.Tools[name]
	if !ok {
		return "", "", fmt.Errorf("pack %s declares no tool %q", m.Name, name)
	}
	for _, l := range t.Lookup {
		switch {
		case l == "path":
			if p, err := exec.LookPath(t.Command); err == nil {
				abs, _ := filepath.Abs(p)
				return abs, "path", nil
			}
		case l == "managed":
			if t.Acquire == nil {
				continue
			}
			dir, err := ManagedBin(m.Name, name, t.Acquire.Version)
			if err != nil {
				continue
			}
			if p, ok := executableIn(dir, t.Command); ok {
				return p, "managed", nil
			}
		case strings.HasPrefix(l, "repo:"):
			rel := filepath.FromSlash(strings.TrimPrefix(l, "repo:"))
			for _, base := range []string{projectDir, repo} {
				if p, ok := executableIn(filepath.Join(base, rel), t.Command); ok {
					return p, "repo", nil
				}
			}
		}
	}
	return "", "", fmt.Errorf("%w: %s (searched %s)", ErrNotFound, t.Command, strings.Join(t.Lookup, ", "))
}

func executableIn(dir, command string) (string, bool) {
	candidates := []string{command}
	if runtime.GOOS == "windows" {
		candidates = []string{command + ".exe", command + ".cmd", command}
	}
	for _, c := range candidates {
		p := filepath.Join(dir, c)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// ProbeVersion runs the tool's version command with a minimal environment.
func ProbeVersion(ctx context.Context, t Tool, path string, env []string) (string, error) {
	args := t.VersionArgs
	exe := path
	res, err := runner.Run(ctx, runner.Cmd{Path: exe, Args: args, Env: env, Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err != nil {
		return "", err
	}
	text := string(res.Stdout) + "\n" + string(res.Stderr)
	m := regexp.MustCompile(`(?m)` + t.VersionRegex).FindStringSubmatch(text)
	if res.ExitCode != 0 || len(m) < 2 {
		return "", fmt.Errorf("cannot determine version of %s (exit %d): %.200s", path, res.ExitCode, strings.TrimSpace(text))
	}
	return m[1], nil
}

// identityCache avoids re-hashing and re-probing unchanged tool files on every review.
type identityCache struct {
	mu      sync.Mutex
	path    string
	Entries map[string]identityEntry `json:"entries"`
}

type identityEntry struct {
	Size     int64  `json:"size"`
	ModTime  int64  `json:"mod_time"`
	Version  string `json:"version"`
	Identity string `json:"identity"`
}

func loadIdentityCache(stateDir string) *identityCache {
	c := &identityCache{path: filepath.Join(stateDir, "tools.json"), Entries: map[string]identityEntry{}}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, c)
		if c.Entries == nil {
			c.Entries = map[string]identityEntry{}
		}
	}
	return c
}

func (c *identityCache) save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, err := json.Marshal(c); err == nil {
		_ = fsutil.WriteFileAtomic(c.path, b, 0o644)
	}
}

// Toolbox resolves tools for a review, verifying each against the lock.
type Toolbox struct {
	Repo     string
	Lock     *config.Lock
	Env      []string
	cache    *identityCache
	mu       sync.Mutex
	resolved map[string]ResolvedTool
}

func NewToolbox(repo, stateDir string, lock *config.Lock, env []string) *Toolbox {
	return &Toolbox{Repo: repo, Lock: lock, Env: env, cache: loadIdentityCache(stateDir), resolved: map[string]ResolvedTool{}}
}

func (tb *Toolbox) Save() { tb.cache.save() }

// Identify locates a tool and computes its identity (file hash plus probed version).
func (tb *Toolbox) Identify(ctx context.Context, m *Manifest, name, projectDir string) (ResolvedTool, error) {
	path, source, err := Locate(m, name, tb.Repo, projectDir)
	if err != nil {
		return ResolvedTool{}, err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ResolvedTool{}, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return ResolvedTool{}, err
	}
	tb.cache.mu.Lock()
	e, ok := tb.cache.Entries[real]
	tb.cache.mu.Unlock()
	if !ok || e.Size != fi.Size() || e.ModTime != fi.ModTime().UnixNano() {
		version, err := ProbeVersion(ctx, m.Tools[name], path, tb.Env)
		if err != nil {
			return ResolvedTool{}, err
		}
		sum, err := fsutil.HashFile(real)
		if err != nil {
			return ResolvedTool{}, err
		}
		e = identityEntry{Size: fi.Size(), ModTime: fi.ModTime().UnixNano(), Version: version, Identity: fsutil.HashStrings(sum, version)}
		tb.cache.mu.Lock()
		tb.cache.Entries[real] = e
		tb.cache.mu.Unlock()
	}
	key := name
	if source == "repo" {
		rel, _ := filepath.Rel(tb.Repo, projectDir)
		key = name + "@" + filepath.ToSlash(rel)
	}
	return ResolvedTool{Pack: m.Name, Name: name, Path: path, Version: e.Version, Identity: e.Identity, Source: source, LockKey: key}, nil
}

// Resolve returns a tool only if it matches the lock exactly; review never accepts an unlocked tool.
func (tb *Toolbox) Resolve(ctx context.Context, m *Manifest, name, projectDir string) (ResolvedTool, error) {
	memo := m.Name + "\x00" + name + "\x00" + projectDir
	tb.mu.Lock()
	if rt, ok := tb.resolved[memo]; ok {
		tb.mu.Unlock()
		return rt, nil
	}
	tb.mu.Unlock()

	rt, err := tb.Identify(ctx, m, name, projectDir)
	if err != nil {
		return rt, err
	}
	pl, ok := tb.Lock.Packs[m.Name]
	if !ok {
		return rt, fmt.Errorf("%w: pack %s has no lock entry; run `pistos plugins install`", ErrNotLocked, m.Name)
	}
	tl, ok := pl.Tools[rt.LockKey]
	if !ok {
		return rt, fmt.Errorf("%w: %s; run `pistos plugins install`", ErrNotLocked, rt.LockKey)
	}
	want, ok := tl.SHA256[Platform()]
	if !ok {
		return rt, fmt.Errorf("%w: %s has no checksum for %s; run `pistos plugins install`", ErrNotLocked, rt.LockKey, Platform())
	}
	if want != rt.Identity || tl.Version != rt.Version {
		return rt, fmt.Errorf("%w: %s is now %s at %s (locked %s); re-run `pistos plugins install` to accept the change", ErrChanged, rt.LockKey, rt.Version, rt.Path, tl.Version)
	}
	tb.mu.Lock()
	tb.resolved[memo] = rt
	tb.mu.Unlock()
	return rt, nil
}
