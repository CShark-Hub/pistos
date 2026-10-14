package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/feedback"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/fsutil"
	"example.com/pistos/internal/report"
	"example.com/pistos/internal/review"
	"example.com/pistos/internal/snapshot"
	"example.com/pistos/internal/store"
)

// htmlReportPath is regenerated with every published report, so it always matches current.json.
func htmlReportPath(repo string) string {
	return filepath.Join(store.Store{Repo: repo}.StateDir(), "report.html")
}

func renderHTML(ctx context.Context, repo string, env *finding.Envelope, token string) ([]byte, error) {
	g := snapshot.Git{Repo: repo}
	tree := env.Canonical.Scope.HeadTree
	var buf bytes.Buffer
	err := report.HTML(&buf, env, report.HTMLOptions{
		Source:  func(p string) ([]byte, error) { return g.Show(ctx, tree, p) },
		Token:   token,
		Version: Version,
	})
	return buf.Bytes(), err
}

// writeHTMLReport writes the static report. It never carries a token: a file on disk cannot prove
// which local process may write decisions, so saving from it shows the equivalent commands instead.
func writeHTMLReport(ctx context.Context, repo string, env *finding.Envelope) (string, error) {
	b, err := renderHTML(ctx, repo, env, "")
	if err != nil {
		return "", err
	}
	p := htmlReportPath(repo)
	return p, fsutil.WriteFileAtomic(p, b, 0o644)
}

// refreshHTMLReport keeps an existing static report in step after tag/reopen refilter the current one.
func (a *app) refreshHTMLReport(ctx context.Context, repo string, env *finding.Envelope) {
	if env == nil {
		return
	}
	if _, err := os.Stat(htmlReportPath(repo)); err != nil {
		return
	}
	if _, err := writeHTMLReport(ctx, repo, env); err != nil {
		fmt.Fprintln(a.stderr, "note: html report not updated:", err)
	}
}

// announceReport tells the person where the report is: a clickable link, the command that opens it
// on this machine, and the command that serves it with saving enabled.
func (a *app) announceReport(path string) {
	v := detectViewer()
	u := v.fileURL(path)
	fmt.Fprintf(a.stderr, "report   %s\n", hyperlink(a.stderr, u, u))
	if v.remote {
		fmt.Fprintln(a.stderr, "         remote session: run `pistos report --no-open --addr 127.0.0.1:8765` and forward it with `ssh -L 8765:127.0.0.1:8765 <this host>`")
		return
	}
	if h := v.hintFor(path); h != "" {
		fmt.Fprintf(a.stderr, "         open: %s\n", h)
	}
	fmt.Fprintln(a.stderr, "         triage in the browser and save decisions: pistos report")
}

func (a *app) reportCommand() *cobra.Command {
	var addr string
	var noOpen, static bool
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Browse the current report and mark issues to ignore in future reviews",
		Long: `Serve the current report on a loopback address and open it in the browser. Select issues and
choose, per issue, whether to ignore this exact issue or every finding of its kind (same family in
the same project, optionally limited to path globs). Saving records the decisions in
.pistos/feedback.yaml, exactly like ` + "`pistos tag`" + `, and refilters the report. The server runs until
interrupted.

--static only regenerates and opens .pistos/state/report.html; that file cannot save decisions and
shows the equivalent pistos tag commands instead.`,
		Example: `  pistos report
  pistos report --no-open --addr 127.0.0.1:8765
  pistos report --static`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			env, err := store.Store{Repo: repo}.LoadCurrent()
			if err != nil {
				return withCode(ExitError, err)
			}
			v := detectViewer()
			if static {
				p, err := writeHTMLReport(ctx, repo, env)
				if err != nil {
					return withCode(ExitError, err)
				}
				fmt.Fprintln(a.stdout, p)
				if !noOpen {
					if err := v.open(p); err != nil {
						a.announceReport(p)
					}
				}
				return nil
			}
			return a.serveReport(ctx, repo, addr, !noOpen, v)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:0", "loopback address to serve on (port 0 picks a free one)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print the address instead of opening a browser")
	cmd.Flags().BoolVar(&static, "static", false, "write and open the static report instead of serving it")
	return cmd
}

type decisionRequest struct {
	Decisions []struct {
		Key    string   `json:"key"`
		Scope  string   `json:"scope"`
		Tags   []string `json:"tags"`
		Reason string   `json:"reason"`
		Paths  []string `json:"paths"`
	} `json:"decisions"`
}

type appliedDecision struct {
	Key         string `json:"key"`
	ID          string `json:"id"`
	Description string `json:"description"`
}

func (a *app) serveReport(ctx context.Context, repo, addr string, open bool, v viewer) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return withCode(ExitError, fmt.Errorf("--addr: %w", err))
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return withCode(ExitError, fmt.Errorf("--addr must be a loopback IP address (127.0.0.1 or ::1): decisions are written without further authentication"))
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return withCode(ExitError, err)
	}
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	token := hex.EncodeToString(tok)
	bound := ln.Addr().String()
	srv := &http.Server{Handler: a.reportHandler(ctx, repo, token, bound), ReadHeaderTimeout: 10 * time.Second}

	u := "http://" + bound + "/"
	fmt.Fprintf(a.stdout, "serving the current report at %s (Ctrl-C to stop)\n", hyperlink(a.stdout, u, u))
	if open {
		if err := v.open(u); err != nil {
			fmt.Fprintln(a.stdout, "open it in a browser; this machine has no browser opener")
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return withCode(ExitError, err)
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		return nil
	}
}

// reportHandler serves the page and the save endpoint. The Host check defeats DNS rebinding; the
// token, sent in a custom header, makes every cross-origin write a failed CORS preflight.
func (a *app) reportHandler(ctx context.Context, repo, token, bound string) http.Handler {
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		env, err := store.Store{Repo: repo}.LoadCurrent()
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		b, err := renderHTML(r.Context(), repo, env, token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
		w.Write(b)
	})
	mux.HandleFunc("POST /api/decisions", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Pistos-Token")), []byte(token)) != 1 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing or wrong token; reload the page"})
			return
		}
		var req decisionRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		applied, errs := a.applyDecisions(ctx, repo, req)
		writeJSON(w, http.StatusOK, map[string]any{"applied": applied, "errors": errs})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != bound {
			http.Error(w, "unexpected Host header", http.StatusMisdirectedRequest)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// applyDecisions records each selection through the same path as `pistos tag`. Selections of the
// same kind collapse into one pattern decision.
func (a *app) applyDecisions(ctx context.Context, repo string, req decisionRequest) ([]appliedDecision, []string) {
	applied := []appliedDecision{}
	errs := []string{}
	seen := map[string]bool{}
	var last *finding.Envelope
	for _, d := range req.Decisions {
		o := review.TagOptions{Tags: d.Tags, Reason: strings.TrimSpace(d.Reason)}
		switch d.Scope {
		case feedback.ScopeIssue:
		case feedback.ScopePattern:
			o.Pattern = true
			for _, p := range d.Paths {
				if p = strings.TrimSpace(p); p != "" {
					o.PatternPaths = append(o.PatternPaths, p)
				}
			}
			ref, err := review.ResolveRef(repo, d.Key)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			kind := ref.Issue.Project + "\x00" + ref.Issue.Family + "\x00" + strings.Join(o.PatternPaths, "\x00")
			if seen[kind] {
				continue
			}
			seen[kind] = true
		default:
			errs = append(errs, fmt.Sprintf("%s: scope must be %q or %q", d.Key, feedback.ScopeIssue, feedback.ScopePattern))
			continue
		}
		dec, env, err := review.Tag(ctx, repo, d.Key, o)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", d.Key, err))
			continue
		}
		if env != nil {
			last = env
		}
		applied = append(applied, appliedDecision{Key: d.Key, ID: dec.ID, Description: dec.Describe()})
		fmt.Fprintf(a.stdout, "saved %s → [%s] (%s)\n", dec.ID, strings.Join(dec.Tags, ", "), dec.Describe())
	}
	a.refreshHTMLReport(ctx, repo, last)
	return applied, errs
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
