package feedback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"

	"example.com/pistos/internal/fsutil"
)

const (
	lockRetry = 20 * time.Millisecond
	lockStale = 30 * time.Second
)

// Update applies fn to the current decisions under an exclusive lock and writes the result
// atomically. Only decisions fn changed are re-encoded; other entries and comments are preserved.
func Update(ctx context.Context, repo string, fn func(f *File) error) (*File, error) {
	unlock, err := acquireLock(ctx, repo)
	if err != nil {
		return nil, err
	}
	defer unlock()

	raw, err := os.ReadFile(Path(repo))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	before, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("refusing to modify invalid feedback: %w", err)
	}
	after, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := fn(after); err != nil {
		return nil, err
	}
	if err := after.Validate(); err != nil {
		return nil, err
	}
	out, err := rewrite(raw, before, after)
	if err != nil {
		return nil, err
	}
	if _, err := Parse(out); err != nil {
		return nil, fmt.Errorf("internal error: rewritten feedback does not validate: %w", err)
	}
	return after, fsutil.WriteFileAtomic(Path(repo), out, 0o644)
}

// rewrite patches the YAML node tree in place so unrelated entries keep their formatting and comments.
func rewrite(raw []byte, before, after *File) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	setScalar(root, "schema", strconv.Itoa(after.Schema), "!!int")
	seq := mappingValue(root, "decisions")
	if seq == nil {
		seq = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "decisions"}, seq)
	}
	seq.Style = 0

	oldByID := map[string]Decision{}
	for _, d := range before.Decisions {
		oldByID[d.ID] = d
	}
	nodeByID := map[string]*yaml.Node{}
	for _, n := range seq.Content {
		if id := mappingValue(n, "id"); id != nil {
			nodeByID[id.Value] = n
		}
	}

	var content []*yaml.Node
	for _, d := range after.Decisions {
		old, existed := oldByID[d.ID]
		if n, ok := nodeByID[d.ID]; ok && existed && equalDecision(old, d) {
			content = append(content, n)
			continue
		}
		var n yaml.Node
		if err := n.Encode(d); err != nil {
			return nil, err
		}
		if prev, ok := nodeByID[d.ID]; ok {
			n.HeadComment, n.LineComment, n.FootComment = prev.HeadComment, prev.LineComment, prev.FootComment
		}
		content = append(content, &n)
	}
	seq.Content = content

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func equalDecision(a, b Decision) bool {
	x, _ := yaml.Marshal(a)
	y, _ := yaml.Marshal(b)
	return bytes.Equal(x, y)
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setScalar(m *yaml.Node, key, value, tag string) {
	if v := mappingValue(m, key); v != nil {
		v.Value, v.Tag = value, tag
		return
	}
	m.Content = append([]*yaml.Node{{Kind: yaml.ScalarNode, Value: key}, {Kind: yaml.ScalarNode, Value: value, Tag: tag}}, m.Content...)
}

// acquireLock serializes mutations across processes with an exclusive lock file.
func acquireLock(ctx context.Context, repo string) (func(), error) {
	p := lockPath(repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	for {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(p) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if fi, serr := os.Stat(p); serr == nil && time.Since(fi.ModTime()) > lockStale {
			os.Remove(p)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for %s: %w", p, ctx.Err())
		case <-time.After(lockRetry):
		}
	}
}
