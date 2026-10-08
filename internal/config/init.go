package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const gitignore = "# Local, regenerable state. Decisions live in feedback.yaml.\ncache/\nstate/\n"

const feedbackSkeleton = `# Pistos review decisions. Created and updated by "pistos tag" / "pistos feedback".
# Entries are readable and editable; generated match selectors should not be authored by hand.
schema: 1
decisions: []
`

type InitResult struct {
	Created  []string
	Kept     []string
	Projects []Project
	// Suggested lists detected projects missing from an existing config; init never edits it.
	Suggested []Project
}

// Init writes missing project files and never overwrites existing configuration, feedback or prompts.
func Init(repo string, langs []string, detected []Project) (*InitResult, error) {
	res := &InitResult{Projects: detected}
	dir := filepath.Join(repo, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	cfgPath := Path(repo, ConfigFile)
	if existing, err := Load(repo); err == nil {
		res.Kept = append(res.Kept, rel(repo, cfgPath))
		have := map[string]bool{}
		for _, p := range existing.Projects {
			have[p.Root+"\x00"+p.Ecosystem] = true
		}
		for _, p := range detected {
			if !have[p.Root+"\x00"+p.Ecosystem] {
				res.Suggested = append(res.Suggested, p)
			}
		}
	} else if _, statErr := os.Stat(cfgPath); statErr == nil {
		return nil, fmt.Errorf("existing %s is invalid and was left untouched: %w", rel(repo, cfgPath), err)
	} else {
		if err := writeNew(cfgPath, []byte(RenderConfig(langs, detected))); err != nil {
			return nil, err
		}
		res.Created = append(res.Created, rel(repo, cfgPath))
	}

	for name, content := range map[string]string{FeedbackFile: feedbackSkeleton, ".gitignore": gitignore} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			res.Kept = append(res.Kept, rel(repo, p))
			continue
		}
		if err := writeNew(p, []byte(content)); err != nil {
			return nil, err
		}
		res.Created = append(res.Created, rel(repo, p))
	}
	sort.Strings(res.Created)
	sort.Strings(res.Kept)
	return res, nil
}

// writeNew refuses to replace a file that appeared concurrently.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s appeared while initializing; not overwriting", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func rel(repo, p string) string {
	r, err := filepath.Rel(repo, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

func RenderConfig(langs []string, projects []Project) string {
	var b strings.Builder
	b.WriteString("# Pistos configuration. See docs/configuration.md.\n")
	b.WriteString("schema = 1\n")
	b.WriteString("languages = " + tomlList(langs) + "\n")
	b.WriteString("profile = \"balanced\"\n\n")
	b.WriteString("[execution]\nmode = \"native\"\njobs = 4\ntimeout = \"10m\"\n\n")
	b.WriteString("[policy]\n# New deterministic findings at or above this severity fail the gate (error, warning, info, none).\nblock_on = \"warning\"\n\n")
	b.WriteString("[metrics]\n# Thresholds for the metrics check (definitions: docs/metrics.md). Advisory unless gate = true.\ngate = false\ncyclomatic = 10\nnesting = 4\nparams = 5\nstatements = 50\n\n")
	b.WriteString("[ai]\n# AI review is advisory and disabled until models are configured.\nenabled = false\nrequired = false\naggregation = \"union\"\nreuse_results = true\n")
	for _, p := range projects {
		b.WriteString("\n[[projects]]\n")
		fmt.Fprintf(&b, "id = %s\nroot = %s\necosystem = %s\nchecks = %s\n",
			strconv.Quote(p.ID), strconv.Quote(p.Root), strconv.Quote(p.Ecosystem), tomlList(p.Checks))
	}
	return b.String()
}

func tomlList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}
