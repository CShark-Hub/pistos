package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"example.com/pistos/internal/fsutil"
)

const (
	LockSchema      = 1
	ProtocolVersion = 1
)

// Lock pins the pack and tool versions a repository reviews with.
// Checksums are keyed by GOOS/GOARCH because tool binaries differ per platform.
type Lock struct {
	Schema   int                 `json:"schema"`
	Protocol int                 `json:"protocol"`
	Packs    map[string]PackLock `json:"packs"`
}

type PackLock struct {
	Version string              `json:"version"`
	Tools   map[string]ToolLock `json:"tools"`
}

type ToolLock struct {
	Version string `json:"version"`
	// Source records how the tool was found: path, repo or managed.
	Source string            `json:"source"`
	SHA256 map[string]string `json:"sha256,omitempty"`
}

func LoadLock(repo string) (*Lock, error) {
	b, err := os.ReadFile(Path(repo, LockFile))
	if errors.Is(err, os.ErrNotExist) {
		return &Lock{Schema: LockSchema, Protocol: ProtocolVersion, Packs: map[string]PackLock{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var l Lock
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("lock.json: %w", err)
	}
	if l.Schema != LockSchema {
		return nil, fmt.Errorf("lock.json schema %d is not supported (expected %d)", l.Schema, LockSchema)
	}
	if l.Protocol != ProtocolVersion {
		return nil, fmt.Errorf("lock.json protocol %d is not supported (expected %d)", l.Protocol, ProtocolVersion)
	}
	if l.Packs == nil {
		l.Packs = map[string]PackLock{}
	}
	return &l, nil
}

func (l *Lock) Save(repo string) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(Path(repo, LockFile), append(b, '\n'), 0o644)
}

func (l *Lock) PackNames() []string {
	names := make([]string, 0, len(l.Packs))
	for n := range l.Packs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
