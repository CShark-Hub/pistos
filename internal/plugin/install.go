package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/runner"
)

type InstallOptions struct {
	Repo     string
	Locked   bool
	Offline  bool
	StateDir string
	Log      func(string)
}

type InstallResult struct {
	Tool    string
	Pack    string
	Version string
	Source  string
	Path    string
	Action  string
	Err     error
}

// acquisitionEnv is the environment for explicit downloads: no provider secrets, but proxy settings.
func acquisitionEnv(extra ...string) []string {
	return append(BaseEnv([]string{"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
		"GOPROXY", "GOPRIVATE", "GONOSUMDB", "GONOPROXY", "GOFLAGS", "GOPATH", "GOMODCACHE", "GOCACHE",
		"NPM_CONFIG_REGISTRY", "UV_INDEX_URL", "PIP_INDEX_URL"}), extra...)
}

// Install resolves (and, where the manifest declares how, acquires) every tool needed by the
// configured checks, then records versions and identities in the lock.
func Install(ctx context.Context, cfg *config.Config, lock *config.Lock, o InstallOptions) ([]InstallResult, error) {
	log := o.Log
	if log == nil {
		log = func(string) {}
	}
	tb := NewToolbox(o.Repo, o.StateDir, lock, BaseEnv(nil))
	defer tb.Save()

	type need struct {
		m          *Manifest
		tool       string
		projectDir string
	}
	var needs []need
	seen := map[string]bool{}
	for _, p := range cfg.Projects {
		eco, _ := config.NormalizeLanguage(p.Ecosystem)
		m, err := LoadManifest(eco)
		if err != nil {
			return nil, err
		}
		for _, check := range p.Checks {
			if _, ok := m.Checks[check]; !ok {
				return nil, fmt.Errorf("project %s: pack %s has no check %q", p.ID, eco, check)
			}
			for _, tool := range m.ToolsFor(check) {
				dir := filepath.Join(o.Repo, filepath.FromSlash(p.Root))
				k := eco + "\x00" + tool + "\x00" + dir
				if !seen[k] {
					seen[k] = true
					needs = append(needs, need{m, tool, dir})
				}
			}
		}
	}
	// Runtimes such as the Go toolchain must be ready before tools built with them are acquired.
	sort.SliceStable(needs, func(i, j int) bool {
		return needs[i].m.Tools[needs[i].tool].Acquire == nil && needs[j].m.Tools[needs[j].tool].Acquire != nil
	})

	var results []InstallResult
	var failed bool
	for _, n := range needs {
		r := InstallResult{Tool: n.tool, Pack: n.m.Name}
		rt, err := tb.Identify(ctx, n.m, n.tool, n.projectDir)
		if errors.Is(err, ErrNotFound) && n.m.Tools[n.tool].Acquire != nil {
			if o.Offline {
				err = fmt.Errorf("%w; acquisition disabled by --offline", err)
			} else {
				r.Action = "acquired"
				log(fmt.Sprintf("acquiring %s %s", n.tool, n.m.Tools[n.tool].Acquire.Version))
				if aerr := acquire(ctx, tb, n.m, n.tool, n.projectDir); aerr != nil {
					err = aerr
				} else {
					rt, err = tb.Identify(ctx, n.m, n.tool, n.projectDir)
				}
			}
		}
		if err != nil {
			r.Err, failed = err, true
			results = append(results, r)
			continue
		}
		r.Version, r.Source, r.Path = rt.Version, rt.Source, rt.Path
		if r.Action == "" {
			r.Action = "found"
		}
		if err := recordLock(lock, rt, o.Locked); err != nil {
			r.Err, failed = err, true
		} else if !o.Locked {
			r.Action += ", locked"
		} else {
			r.Action += ", matches lock"
		}
		results = append(results, r)
	}
	if failed {
		return results, errors.New("some tools could not be installed or verified")
	}
	return results, nil
}

func recordLock(lock *config.Lock, rt ResolvedTool, locked bool) error {
	pl := lock.Packs[rt.Pack]
	m, _ := LoadManifest(rt.Pack)
	if pl.Tools == nil {
		pl.Tools = map[string]config.ToolLock{}
	}
	tl, exists := pl.Tools[rt.LockKey]
	if locked {
		switch {
		case !exists:
			return fmt.Errorf("%s is not in lock.json (--locked forbids adding it)", rt.LockKey)
		case tl.Version != rt.Version:
			return fmt.Errorf("%s is %s but lock.json requires %s", rt.LockKey, rt.Version, tl.Version)
		case tl.SHA256[Platform()] == "":
			return fmt.Errorf("%s has no checksum for %s in lock.json (--locked forbids adding it)", rt.LockKey, Platform())
		case tl.SHA256[Platform()] != rt.Identity:
			return fmt.Errorf("%s at %s does not match the locked checksum", rt.LockKey, rt.Path)
		}
		return nil
	}
	if !exists || tl.Version != rt.Version {
		tl = config.ToolLock{Version: rt.Version, SHA256: map[string]string{}}
	}
	tl.Source = rt.Source
	if tl.SHA256 == nil {
		tl.SHA256 = map[string]string{}
	}
	tl.SHA256[Platform()] = rt.Identity
	pl.Tools[rt.LockKey] = tl
	if m != nil {
		pl.Version = m.Version
	}
	lock.Packs[rt.Pack] = pl
	return nil
}

func acquire(ctx context.Context, tb *Toolbox, m *Manifest, tool, projectDir string) error {
	a := m.Tools[tool].Acquire
	bin, err := ManagedBin(m.Name, tool, a.Version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	switch a.Method {
	case "go-install":
		goTool, err := tb.Identify(ctx, m, "go", projectDir)
		if err != nil {
			return fmt.Errorf("go-install needs the Go toolchain: %w", err)
		}
		return runAcquire(ctx, goTool.Path, []string{"install", a.Package + "@" + a.Version}, "", acquisitionEnv("GOBIN="+bin, "GOTOOLCHAIN=local", "CGO_ENABLED=0"))
	default:
		return fmt.Errorf("unsupported acquisition method %q", a.Method)
	}
}

func runAcquire(ctx context.Context, exe string, args []string, dir string, env []string) error {
	res, err := runner.Run(ctx, runner.Cmd{Path: exe, Args: args, Dir: dir, Env: env, Timeout: 15 * time.Minute})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s %s failed: %s", filepath.Base(exe), strings.Join(args, " "), tail(strings.Split(string(res.Stderr)+string(res.Stdout), "\n")))
	}
	return nil
}
