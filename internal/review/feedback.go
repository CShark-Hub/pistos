package review

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/feedback"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/plugin"
	"example.com/pistos/internal/snapshot"
	"example.com/pistos/internal/store"
)

func (e *engine) loadFeedback(ctx context.Context, s finding.Scope) (*feedback.File, string, error) {
	if !e.opts.FeedbackFromBase {
		f, err := feedback.Load(e.opts.Repo)
		return f, "", err
	}
	if s.BaseTree == "" {
		return nil, "", fmt.Errorf("--feedback-from-base needs a baseline (not --all)")
	}
	rel := config.Dir + "/" + config.FeedbackFile
	base, err := e.git.Show(ctx, s.BaseTree, rel)
	if err != nil {
		base = nil
	}
	f, err := feedback.Parse(base)
	if err != nil {
		return nil, "", fmt.Errorf("baseline %w", err)
	}
	note := "decisions taken from the baseline's feedback.yaml"
	if e.git.ObjectID(ctx, s.BaseTree, rel) != e.git.ObjectID(ctx, s.HeadTree, rel) {
		note += "; this change modifies feedback.yaml and those edits were NOT applied — review them separately"
	}
	return f, note, nil
}

// matchEnvironment exposes verified renames and test-path classification to feedback matching.
func (e *engine) matchEnvironment(ctx context.Context, headTree string) feedback.Environment {
	return MatchEnvironment(ctx, e.git, e.cfg, headTree)
}

func MatchEnvironment(ctx context.Context, g snapshot.Git, cfg *config.Config, headTree string) feedback.Environment {
	var mu sync.Mutex
	renames := map[string]map[string]string{}
	renamesFrom := func(origin string) map[string]string {
		mu.Lock()
		defer mu.Unlock()
		if m, ok := renames[origin]; ok {
			return m
		}
		m := map[string]string{}
		if changes, err := g.Diff(ctx, origin, headTree); err == nil {
			for _, c := range changes {
				if c.Status == "R" {
					m[c.From] = c.Path
				}
			}
		}
		renames[origin] = m
		return m
	}
	return feedback.Environment{
		Renamed: func(d *feedback.Decision, newPath string) bool {
			mt := d.Match
			if g.ObjectID(ctx, headTree, mt.Path) != "" {
				return false
			}
			if mt.FileBlob != "" && g.ObjectID(ctx, headTree, newPath) == mt.FileBlob {
				return true
			}
			if d.Origin == nil {
				return false
			}
			origin := d.Origin.Tree
			if origin == "" || !g.Exists(ctx, origin) {
				origin = ""
				if d.Origin.Commit != "" {
					_, origin, _ = g.Commit(ctx, d.Origin.Commit)
				}
			}
			return origin != "" && renamesFrom(origin)[mt.Path] == newPath
		},
		IsTestPath: func(project, p string) bool {
			for _, pr := range cfg.Projects {
				if pr.ID != project {
					continue
				}
				eco, _ := config.NormalizeLanguage(pr.Ecosystem)
				m, err := plugin.LoadManifest(eco)
				if err != nil {
					return false
				}
				root, _ := config.CleanRel(pr.Root)
				rel := p
				if root != "." {
					rel = strings.TrimPrefix(p, root+"/")
				}
				return m.IsTestPath(rel) || m.IsTestPath(path.Clean(p))
			}
			return false
		},
	}
}

// Refilter reapplies the current feedback to the stored report without running any tool or model.
func Refilter(ctx context.Context, repo string) (*finding.Envelope, error) {
	st := store.Store{Repo: repo}
	env, err := st.LoadCurrent()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(repo)
	if err != nil {
		return nil, err
	}
	fb, err := feedback.Load(repo)
	if err != nil {
		return nil, err
	}
	rep := env.Canonical
	rep.Feedback = fb.Hash()
	Finalize(rep, cfg.Policy.BlockOn, feedback.NewMatcher(fb, MatchEnvironment(ctx, snapshot.Git{Repo: repo}, cfg, rep.Scope.HeadTree)).Apply)
	return env, st.SaveCurrent(env)
}
