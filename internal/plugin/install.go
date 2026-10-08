package plugin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/fsutil"
	"example.com/pistos/internal/packs"
	"example.com/pistos/internal/runner"
)

type InstallOptions struct {
	Repo     string
	Locked   bool
	Offline  bool
	StateDir string
	Log      func(string)
	// LSP also installs and locks the language servers of the active packs.
	LSP bool
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
	var images []InstallResult
	if cfg.Execution.Mode == "docker" {
		rt, err := NewContainerRuntime()
		if err != nil {
			return nil, err
		}
		tb.Container = rt
		if images, err = ensureImages(ctx, rt, cfg, lock, o, log); err != nil {
			return images, err
		}
	}

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
			c, ok := m.Checks[check]
			if !ok {
				return nil, fmt.Errorf("project %s: pack %s has no check %q", p.ID, eco, check)
			}
			if c.Unsupported != "" {
				log(fmt.Sprintf("%s: %s is unsupported: %s", p.ID, check, c.Unsupported))
				continue
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
	if o.LSP {
		for _, p := range cfg.Projects {
			eco, _ := config.NormalizeLanguage(p.Ecosystem)
			m, _ := LoadManifest(eco)
			if m == nil || m.LSP == nil {
				continue
			}
			names := append([]string{m.LSP.Tool, m.Tools[m.LSP.Tool].Runtime}, m.LSP.Uses...)
			for _, tool := range names {
				dir := filepath.Join(o.Repo, filepath.FromSlash(p.Root))
				k := eco + "\x00" + tool + "\x00" + dir
				if tool != "" && !seen[k] {
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

	results := images
	var failed bool
	for _, n := range needs {
		r := InstallResult{Tool: n.tool, Pack: n.m.Name}
		rt, err := tb.Identify(ctx, n.m, n.tool, n.projectDir)
		stale := errors.Is(err, ErrChanged) && n.m.Tools[n.tool].VersionFrom == "pack"
		if (errors.Is(err, ErrNotFound) || stale) && n.m.Tools[n.tool].Acquire != nil {
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
	root, err := ManagedRoot(m.Name, tool, a.Version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	switch a.Method {
	case "go-install":
		goTool, err := tb.Identify(ctx, m, "go", projectDir)
		if err != nil {
			return fmt.Errorf("go-install needs the Go toolchain: %w", err)
		}
		return runAcquire(ctx, goTool.Path, []string{"install", a.Package + "@" + a.Version}, "",
			acquisitionEnv("GOBIN="+filepath.Join(root, "bin"), "GOTOOLCHAIN=local", "CGO_ENABLED=0"))
	case "npm":
		node, err := tb.Identify(ctx, m, "node", projectDir)
		if err != nil {
			return fmt.Errorf("npm acquisition needs Node.js: %w", err)
		}
		npm := npmFor(node.Path)
		pkg, _, _ := strings.Cut(a.Package, ":")
		// Install scripts are disabled: acquisition must not execute arbitrary package code.
		return runAcquire(ctx, npm, []string{"install", "--prefix", root, "--no-save", "--ignore-scripts", "--no-audit", "--no-fund", pkg + "@" + a.Version}, root,
			acquisitionEnv("PATH="+filepath.Dir(node.Path)+string(os.PathListSeparator)+os.Getenv("PATH")))
	case "pip":
		python, err := tb.Identify(ctx, m, "python", projectDir)
		if err != nil {
			return fmt.Errorf("pip acquisition needs Python: %w", err)
		}
		venv := filepath.Join(root, "venv")
		if err := runAcquire(ctx, python.Path, []string{"-m", "venv", venv}, root, acquisitionEnv()); err != nil {
			return err
		}
		pip := filepath.Join(venv, "bin", "pip")
		if runtime.GOOS == "windows" {
			pip = filepath.Join(venv, "Scripts", "pip.exe")
		}
		return runAcquire(ctx, pip, []string{"install", "--disable-pip-version-check", "--no-input", a.Package + "==" + a.Version}, root, acquisitionEnv())
	case "embedded":
		if err := copyEmbedded(a.Files, root); err != nil {
			return err
		}
		if len(a.Deps) == 0 {
			return nil
		}
		node, err := tb.Identify(ctx, m, "node", projectDir)
		if err != nil {
			return fmt.Errorf("worker dependencies need Node.js: %w", err)
		}
		args := append([]string{"install", "--prefix", root, "--no-save", "--ignore-scripts", "--no-audit", "--no-fund"}, a.Deps...)
		return runAcquire(ctx, npmFor(node.Path), args, root, acquisitionEnv("PATH="+filepath.Dir(node.Path)+string(os.PathListSeparator)+os.Getenv("PATH")))
	case "dotnet-embedded":
		dotnet, err := tb.Identify(ctx, m, "dotnet", projectDir)
		if err != nil {
			return fmt.Errorf("building the worker needs the .NET SDK: %w", err)
		}
		src := filepath.Join(root, "src")
		if err := copyEmbedded(a.Files, src); err != nil {
			return err
		}
		major, _, _ := strings.Cut(dotnet.Version, ".")
		empty := filepath.Join(root, "empty-feed")
		_ = os.MkdirAll(empty, 0o755)
		// The worker references the SDK's bundled Roslyn, so restore needs no package source.
		return runAcquire(ctx, dotnet.Path, []string{"build", "-c", "Release", "-nologo", "-tl:off", "-nodeReuse:false",
			"-o", filepath.Join(root, "bin"), "-p:PistosTargetFramework=net" + major + ".0", "-p:RestoreSources=" + empty, "-p:UseSharedCompilation=false"},
			src, acquisitionEnv("DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1"))
	case "dotnet-tool":
		dotnet, err := tb.Identify(ctx, m, "dotnet", projectDir)
		if err != nil {
			return fmt.Errorf("dotnet tool install needs the .NET SDK: %w", err)
		}
		return runAcquire(ctx, dotnet.Path, []string{"tool", "install", a.Package, "--version", a.Version, "--tool-path", filepath.Join(root, "bin")},
			root, acquisitionEnv("DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1"))
	default:
		return fmt.Errorf("unsupported acquisition method %q", a.Method)
	}
}

func npmFor(node string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(filepath.Dir(node), "npm.cmd")
	}
	return filepath.Join(filepath.Dir(node), "npm")
}

func copyEmbedded(files []string, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		b, err := fs.ReadFile(packs.FS, f)
		if err != nil {
			return err
		}
		if err := fsutil.WriteFileAtomic(filepath.Join(dir, filepath.Base(f)), b, 0o644); err != nil {
			return err
		}
	}
	return nil
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

// ensureImages builds missing pack images and records their IDs. Under --locked an image whose ID
// differs from the lock is an error: a rebuilt image is a changed toolchain.
func ensureImages(ctx context.Context, rt *ContainerRuntime, cfg *config.Config, lock *config.Lock, o InstallOptions, log func(string)) ([]InstallResult, error) {
	var out []InstallResult
	seen := map[string]bool{}
	for _, p := range cfg.Projects {
		eco, _ := config.NormalizeLanguage(p.Ecosystem)
		if seen[eco] {
			continue
		}
		seen[eco] = true
		m, err := LoadManifest(eco)
		if err != nil {
			return out, err
		}
		r := InstallResult{Pack: eco, Tool: "image", Version: ImageTag(m), Source: "docker"}
		id := rt.ImageID(ctx, m)
		if id == "" {
			if o.Offline {
				r.Err = fmt.Errorf("image %s is not built and --offline forbids pulling base images", ImageTag(m))
				return append(out, r), r.Err
			}
			log("building image " + ImageTag(m))
			if id, err = rt.Build(ctx, m); err != nil {
				r.Err = err
				return append(out, r), err
			}
			r.Action = "built"
		} else {
			r.Action = "found"
		}
		r.Path = id
		pl := lock.Packs[eco]
		if o.Locked {
			if pl.Image == nil || pl.Image.ID[Platform()] != id {
				r.Err = fmt.Errorf("image %s is %s but lock.json records a different image", ImageTag(m), id)
				return append(out, r), r.Err
			}
		} else {
			if pl.Image == nil || pl.Image.Tag != ImageTag(m) {
				pl.Image = &config.ImageLock{Tag: ImageTag(m), ID: map[string]string{}}
			}
			pl.Image.ID[Platform()] = id
			pl.Version = m.Version
			if pl.Tools == nil {
				pl.Tools = map[string]config.ToolLock{}
			}
			lock.Packs[eco] = pl
		}
		out = append(out, r)
	}
	return out, nil
}
