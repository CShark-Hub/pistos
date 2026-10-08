// Package store keeps regenerable local state: the content-addressed result cache, the issue ledger
// and the current published report. Deleting it never loses human decisions.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/fsutil"
)

const cacheSchema = 1

type Store struct{ Repo string }

func (s Store) CacheDir() string { return filepath.Join(s.Repo, config.Dir, "cache") }
func (s Store) StateDir() string { return filepath.Join(s.Repo, config.Dir, "state") }

type cachedResult struct {
	Schema int                      `json:"schema"`
	Key    string                   `json:"key"`
	Result finding.CapabilityResult `json:"result"`
}

func (s Store) resultPath(key string) string {
	return filepath.Join(s.CacheDir(), "results", key[:2], key+".json")
}

// GetResult returns a cached completed result; anything malformed or mismatched is treated as a miss.
func (s Store) GetResult(key string) (*finding.CapabilityResult, bool) {
	b, err := os.ReadFile(s.resultPath(key))
	if err != nil {
		return nil, false
	}
	var c cachedResult
	if json.Unmarshal(b, &c) != nil || c.Schema != cacheSchema || c.Key != key || !c.Result.Status.Completed() {
		return nil, false
	}
	return &c.Result, true
}

// PutResult stores only completed results, so a failed analyzer can never be replayed as clean.
func (s Store) PutResult(key string, r finding.CapabilityResult) error {
	if r.Status != finding.StatusComplete {
		return fmt.Errorf("refusing to cache %s result", r.Status)
	}
	b, err := json.Marshal(cachedResult{Schema: cacheSchema, Key: key, Result: r})
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.resultPath(key), b, 0o644)
}

// Ledger assigns stable local display numbers to canonical issue keys.
type Ledger struct {
	Next    int                    `json:"next"`
	Numbers map[string]int         `json:"numbers"`
	Seen    map[string]LedgerEntry `json:"seen"`
}

// LedgerEntry keeps the last evidence for a key so it can be tagged without a current report.
type LedgerEntry struct {
	Number  int             `json:"number"`
	Finding finding.Finding `json:"finding"`
	Tree    string          `json:"tree"`
}

func (s Store) LoadLedger() *Ledger {
	l := &Ledger{Next: 1, Numbers: map[string]int{}, Seen: map[string]LedgerEntry{}}
	if b, err := os.ReadFile(filepath.Join(s.StateDir(), "ledger.json")); err == nil {
		_ = json.Unmarshal(b, l)
	}
	if l.Numbers == nil {
		l.Numbers = map[string]int{}
	}
	if l.Seen == nil {
		l.Seen = map[string]LedgerEntry{}
	}
	if l.Next < 1 {
		l.Next = 1
	}
	return l
}

func (s Store) SaveLedger(l *Ledger) error {
	b, err := json.MarshalIndent(l, "", " ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(s.StateDir(), "ledger.json"), b, 0o644)
}

// Number returns the display number for key, assigning the next free one in key order for new keys.
func (l *Ledger) Number(key string) int {
	if n, ok := l.Numbers[key]; ok {
		return n
	}
	n := l.Next
	l.Numbers[key] = n
	l.Next++
	return n
}

// AssignAll numbers issues in canonical order so first-time numbering is deterministic.
func (l *Ledger) AssignAll(issues []finding.Issue, tree string) map[string]int {
	out := map[string]int{}
	for _, is := range issues {
		n := l.Number(is.Key)
		out[is.Key] = n
		l.Seen[is.Key] = LedgerEntry{Number: n, Finding: is.Finding, Tree: tree}
	}
	return out
}

func (l *Ledger) KeyForNumber(n int) (string, bool) {
	var keys []string
	for k, v := range l.Numbers {
		if v == n {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) != 1 {
		return "", false
	}
	return keys[0], true
}

func (s Store) currentPath() string { return filepath.Join(s.StateDir(), "current.json") }

// SaveCurrent records the last published report; short references resolve only against it.
func (s Store) SaveCurrent(env *finding.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.currentPath(), b, 0o644)
}

var ErrNoCurrentReport = errors.New("no current report; run `pistos review` first or pass a canonical issue key")

func (s Store) LoadCurrent() (*finding.Envelope, error) {
	b, err := os.ReadFile(s.currentPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoCurrentReport
	}
	if err != nil {
		return nil, err
	}
	var env finding.Envelope
	if err := json.Unmarshal(b, &env); err != nil || env.Canonical == nil {
		return nil, fmt.Errorf("current report is unreadable; run `pistos review` again")
	}
	return &env, nil
}
