// Package snapshot captures immutable git trees for base and head and materializes them for analysis
// without modifying the user's index or working tree.
package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EmptyTree is git's well-known empty tree, used as the baseline of a repository without commits.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

type Git struct{ Repo string }

func (g Git) run(ctx context.Context, env []string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "gc.auto=0", "-c", "core.quotepath=off"}, args...)...)
	cmd.Dir = g.Repo
	// Optional locks would let read-only commands refresh the user's index.
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"), env...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (g Git) output(ctx context.Context, args ...string) (string, error) {
	out, err := g.run(ctx, nil, nil, args...)
	return strings.TrimSpace(string(out)), err
}

func (g Git) IsRepo(ctx context.Context) bool {
	_, err := g.output(ctx, "rev-parse", "--git-dir")
	return err == nil
}

// Commit resolves a ref to its commit and tree IDs.
func (g Git) Commit(ctx context.Context, ref string) (commit, tree string, err error) {
	commit, err = g.output(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || commit == "" {
		return "", "", fmt.Errorf("cannot resolve %q to a commit", ref)
	}
	tree, err = g.output(ctx, "rev-parse", commit+"^{tree}")
	return commit, tree, err
}

func (g Git) HasHead(ctx context.Context) bool {
	_, _, err := g.Commit(ctx, "HEAD")
	return err == nil
}

func (g Git) MergeBase(ctx context.Context, a, b string) (string, error) {
	mb, err := g.output(ctx, "merge-base", a, b)
	if err != nil || mb == "" {
		return "", fmt.Errorf("no merge-base between %s and %s", a, b)
	}
	return mb, nil
}

// WorkingTree writes the working snapshot (staged, unstaged and untracked non-ignored files) as a tree
// object using a throwaway copy of the index, so the user's index stays untouched.
func (g Git) WorkingTree(ctx context.Context) (string, error) {
	indexPath, err := g.output(ctx, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(g.Repo, indexPath)
	}
	tmp, err := os.CreateTemp("", "pistos-index-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if src, err := os.Open(indexPath); err == nil {
		_, err = io.Copy(tmp, src)
		src.Close()
		if err != nil {
			tmp.Close()
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	env := []string{"GIT_INDEX_FILE=" + tmpPath}
	if fi, _ := os.Stat(tmpPath); fi.Size() == 0 {
		os.Remove(tmpPath)
	}
	if _, err := g.run(ctx, env, nil, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	out, err := g.run(ctx, env, nil, "write-tree")
	return strings.TrimSpace(string(out)), err
}

type Change struct {
	Status string `json:"status"`
	Path   string `json:"path"`
	From   string `json:"from,omitempty"`
}

// Diff lists file changes between two trees with git's rename detection.
func (g Git) Diff(ctx context.Context, base, head string) ([]Change, error) {
	out, err := g.run(ctx, nil, nil, "diff-tree", "-r", "-z", "-M", "--name-status", base, head)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	var changes []Change
	for i := 0; i < len(fields); i++ {
		st := fields[i]
		if st == "" {
			continue
		}
		c := Change{Status: st[:1]}
		if c.Status == "R" || c.Status == "C" {
			if i+2 >= len(fields) {
				break
			}
			c.From, c.Path = fields[i+1], fields[i+2]
			i += 2
		} else {
			if i+1 >= len(fields) {
				break
			}
			c.Path = fields[i+1]
			i++
		}
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// ObjectID returns the tree or blob ID at path inside tree, or "" when absent.
func (g Git) ObjectID(ctx context.Context, tree, p string) string {
	if p == "." || p == "" {
		return tree
	}
	id, err := g.output(ctx, "rev-parse", "--verify", "--quiet", tree+":"+p)
	if err != nil {
		return ""
	}
	return id
}

// Materialize extracts tree into dir once; an existing complete materialization is reused because
// tree contents are immutable.
func (g Git) Materialize(ctx context.Context, tree, dir string) error {
	marker := filepath.Join(dir, ".pistos-tree")
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == tree {
		now := time.Now()
		_ = os.Chtimes(dir, now, now)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", tree)
	cmd.Dir = g.Repo
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	extractErr := extract(tar.NewReader(stdout), tmp)
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive %s: %v: %s", tree, err, stderr.String())
	}
	if extractErr != nil {
		return extractErr
	}
	if err := os.WriteFile(filepath.Join(tmp, ".pistos-tree"), []byte(tree+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if b, rerr := os.ReadFile(marker); rerr == nil && strings.TrimSpace(string(b)) == tree {
			return nil
		}
		return err
	}
	return nil
}

func extract(tr *tar.Reader, dest string) error {
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Links escaping the snapshot would let analyzers read outside the authorized root.
			resolved := path.Clean(path.Join(path.Dir(name), h.Linkname))
			if path.IsAbs(h.Linkname) || resolved == ".." || strings.HasPrefix(resolved, "../") {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// Prune keeps the most recently used materializations under root.
func Prune(root string, keep int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type dir struct {
		name string
		mod  time.Time
	}
	var dirs []dir
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			dirs = append(dirs, dir{e.Name(), fi.ModTime()})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	for i := keep; i < len(dirs); i++ {
		_ = os.RemoveAll(filepath.Join(root, dirs[i].name))
	}
}
