package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"example.com/pistos/internal/testutil/gitrepo"
)

func TestWorkingTreeCapturesAllChangesWithoutTouchingIndex(t *testing.T) {
	r := gitrepo.New(t, "go-clean")
	ctx := context.Background()
	g := Git{Repo: r.Dir}
	_, headTree, err := g.Commit(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	r.Write(".gitignore", "ignored.txt\n")
	r.Write("ignored.txt", "secret build output")
	r.Write("staged.go", "package main\n")
	r.Git("add", "staged.go")
	r.Write("main.go", r.Read("main.go")+"\n// unstaged\n")
	r.Write("untracked/new.go", "package untracked\n")
	statusBefore := r.Git("status", "--porcelain")

	tree, err := g.WorkingTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tree == headTree {
		t.Fatal("working tree must differ from HEAD")
	}
	if r.Git("status", "--porcelain") != statusBefore {
		t.Fatal("capturing the working snapshot changed the user's index")
	}
	changes, err := g.Diff(ctx, headTree, tree)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Status
	}
	for _, want := range []string{".gitignore", "main.go", "staged.go", "untracked/new.go"} {
		if got[want] == "" {
			t.Errorf("missing change for %s: %v", want, changes)
		}
	}
	if got["ignored.txt"] != "" {
		t.Error("ignored file captured")
	}

	dir := filepath.Join(t.TempDir(), "work", tree)
	if err := g.Materialize(ctx, tree, dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "untracked", "new.go")); err != nil || string(b) != "package untracked\n" {
		t.Fatalf("materialized content: %q %v", b, err)
	}
	r.Write("untracked/new.go", "package changed\n")
	if err := g.Materialize(ctx, tree, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "untracked", "new.go")); string(b) != "package untracked\n" {
		t.Fatal("materialization must be isolated from later working-tree edits")
	}
}

func TestDiffDetectsRenames(t *testing.T) {
	r := gitrepo.New(t, "go-clean")
	ctx := context.Background()
	g := Git{Repo: r.Dir}
	_, base, _ := g.Commit(ctx, "HEAD")
	r.Git("mv", "main.go", "app.go")
	r.Commit("rename")
	_, head, _ := g.Commit(ctx, "HEAD")
	changes, err := g.Diff(ctx, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Status != "R" || changes[0].From != "main.go" || changes[0].Path != "app.go" {
		t.Fatalf("%+v", changes)
	}
}

func TestRepositoryWithoutCommits(t *testing.T) {
	dir := t.TempDir()
	g := Git{Repo: dir}
	ctx := context.Background()
	if out, err := g.run(ctx, nil, nil, "init", "-q"); err != nil {
		t.Fatal(string(out), err)
	}
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	if g.HasHead(ctx) {
		t.Fatal("no HEAD expected")
	}
	tree, err := g.WorkingTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := g.Diff(ctx, EmptyTree, tree)
	if err != nil || len(changes) != 1 || changes[0].Path != "a.go" {
		t.Fatalf("%+v %v", changes, err)
	}
}
