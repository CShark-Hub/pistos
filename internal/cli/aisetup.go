package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"example.com/pistos/internal/ai"
	"example.com/pistos/internal/config"
	"example.com/pistos/internal/fsutil"
)

// openRouterEndpoint is a variable so tests can point the OpenRouter flow at a fake server.
var openRouterEndpoint = ai.OpenRouterEndpoint

const (
	defaultPasses = 3
	maxPasses     = 8
	keyAttempts   = 3
)

var errInputEnded = errors.New("input ended before setup finished; nothing was written")

// prompter reads answers line by line; secrets are read without echo when input is a terminal.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
	tty *os.File
}

func (a *app) newPrompter() *prompter {
	p := &prompter{in: bufio.NewReader(a.stdin), out: a.stdout}
	if f, ok := a.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		p.tty = f
	}
	return p
}

func (p *prompter) line(q, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", q, def)
	} else {
		fmt.Fprintf(p.out, "%s: ", q)
	}
	s, err := p.in.ReadString('\n')
	if err != nil && (err != io.EOF || s == "") {
		fmt.Fprintln(p.out)
		return "", errInputEnded
	}
	if s = strings.TrimSpace(s); s == "" {
		return def, nil
	}
	return s, nil
}

func (p *prompter) secret(q string) (string, error) {
	if p.tty == nil {
		return p.line(q, "")
	}
	fmt.Fprintf(p.out, "%s: ", q)
	b, err := term.ReadPassword(int(p.tty.Fd()))
	fmt.Fprintln(p.out)
	if err != nil {
		return "", errInputEnded
	}
	return strings.TrimSpace(string(b)), nil
}

func (p *prompter) yes(q string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		s, err := p.line(q+" ("+hint+")", "")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.out, "  please answer y or n")
	}
}

func (p *prompter) number(q string, def, lo, hi int) (int, error) {
	for {
		s, err := p.line(q, strconv.Itoa(def))
		if err != nil {
			return 0, err
		}
		n, err := strconv.Atoi(s)
		if err == nil && n >= lo && n <= hi {
			return n, nil
		}
		fmt.Fprintf(p.out, "  enter a number from %d to %d\n", lo, hi)
	}
}

func (a *app) aiCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "ai", Short: "Configure optional AI review"}
	cmd.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "Interactively connect an OpenAI-compatible endpoint or OpenRouter and choose review models",
		Long: `Asks for the provider, then its endpoint and API key (both are checked against the endpoint
without spending tokens), how many models review each change (independent passes, combined and then
triaged; 3 by default) and which model each pass and the triage use. Writes the [ai], [[ai.discovery]],
[ai.triage] and [models.*] tables of .pistos/config.toml after showing them; [ai.budget] and every
other table are left untouched.

config.toml only names an environment variable. The key itself is either kept in your environment or,
if you agree, saved in your user configuration directory (pistos/credentials.json, readable only by
you) and used when that variable is not set. It is never written into the repository.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			if err := a.runAISetup(cmd.Context(), repo, a.newPrompter()); err != nil {
				return withCode(ExitError, err)
			}
			return nil
		},
	})
	return cmd
}

// offerAISetup runs at the end of `plugins install` on an interactive terminal when AI is still off.
func (a *app) offerAISetup(ctx context.Context, repo string, cfg *config.Config) {
	if cfg.AI.Enabled || !isTerminal(a.stdin) {
		return
	}
	p := a.newPrompter()
	fmt.Fprintln(a.stdout)
	ok, err := p.yes("Configure optional AI review (an OpenAI-compatible endpoint or OpenRouter) now?", false)
	if err != nil || !ok {
		fmt.Fprintln(a.stdout, "skipped; run `pistos ai setup` any time")
		return
	}
	if err := a.runAISetup(ctx, repo, p); err != nil {
		fmt.Fprintln(a.stderr, "pistos: ai setup:", err)
	}
}

type providerChoice struct {
	openRouter bool
	endpoint   string
	key        string
	keyEnv     string
	saveKey    bool
	models     []ai.ModelInfo
}

func (a *app) runAISetup(ctx context.Context, repo string, p *prompter) error {
	path := config.Path(repo, config.ConfigFile)
	src, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no %s/%s found; run `pistos init` first", config.Dir, config.ConfigFile)
		}
		return err
	}
	cfg, err := config.Parse(src)
	if err != nil {
		return err
	}
	out := a.stdout
	fmt.Fprintln(out, "AI review setup. AI issues are advisory: they never block the gate.")
	if cfg.AI.Enabled {
		var ids []string
		for _, d := range cfg.AI.Discovery {
			ids = append(ids, cfg.Models[d.Model].Model)
		}
		fmt.Fprintf(out, "AI review is already configured with %d pass(es): %s\n", len(ids), strings.Join(ids, ", "))
		if ok, err := p.yes("Replace that configuration?", false); err != nil || !ok {
			if err == nil {
				fmt.Fprintln(out, "kept the existing configuration")
			}
			return err
		}
	}

	pc, err := a.chooseProvider(ctx, p)
	if err != nil {
		return err
	}
	setup, err := a.chooseModels(p, pc)
	if err != nil {
		return err
	}
	setup.Required = cfg.AI.Required
	setup.ReuseResults = cfg.AI.ReuseResults == nil || *cfg.AI.ReuseResults

	if cfg.AI.Budget.MaxReviewUSD > 0 {
		for _, m := range setup.Models {
			if !m.Priced() {
				return fmt.Errorf("ai.budget.max_review_usd is set, but the endpoint publishes no prices for %s; nothing was written (remove the cap, or rerun setup and add prompt_usd_per_mtok / completion_usd_per_mtok to [models.%s] by hand afterwards)", m.Model, m.Alias)
			}
		}
	}
	next, err := config.RewriteAI(src, setup)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "\nThis replaces the AI tables of .pistos/config.toml with:")
	fmt.Fprintln(out, indent(config.RenderAI(setup)))
	fmt.Fprintln(out, reviewCostEstimate(setup, cfg.AI.Budget))
	if ok, err := p.yes("Write it?", true); err != nil || !ok {
		if err == nil {
			fmt.Fprintln(out, "nothing was written")
		}
		return err
	}
	if pc.saveKey {
		where, err := ai.SaveKey(pc.keyEnv, pc.key)
		if err != nil {
			return fmt.Errorf("saving the key: %w", err)
		}
		fmt.Fprintf(out, "saved the key as %s in %s\n", pc.keyEnv, where)
	}
	if err := fsutil.WriteFileAtomic(path, next, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "updated %s/%s\n", config.Dir, config.ConfigFile)
	if pc.key != "" && !pc.saveKey && os.Getenv(pc.keyEnv) != pc.key {
		fmt.Fprintf(out, "set the key before reviewing:  export %s=<your key>\n", pc.keyEnv)
	}
	fmt.Fprintln(out, "next: pistos review --profile thorough")
	return nil
}

// assumedCompletionTokens is used for the estimate when no max_tokens bounds completions.
const assumedCompletionTokens = 4096

// reviewCostEstimate prices the most expensive review the configuration allows without a cap: every
// pass sends the full evidence budget (a token per three characters) and uses its whole completion
// allowance, and model triage costs one more call of that size. Small changes cost far less.
func reviewCostEstimate(s config.AISetup, b config.AIBudget) string {
	models := map[string]config.ModelAlias{}
	for _, m := range s.Models {
		models[m.Alias] = m.ModelAlias
	}
	evidence := b.MaxEvidenceChars
	if evidence == 0 {
		evidence = 200_000
	}
	completion := b.MaxTokens
	if completion == 0 {
		completion = assumedCompletionTokens
	}
	aliases := make([]string, 0, len(s.Passes)+1)
	for _, p := range s.Passes {
		aliases = append(aliases, p.Model)
	}
	if s.Triage.Mode == "model" {
		aliases = append(aliases, s.Triage.Model)
	}
	usd := 0.0
	for _, a := range aliases {
		m := models[a]
		if !m.Priced() {
			return "Estimated cost per review: unknown (the endpoint publishes no prices for " + m.Model + ")."
		}
		usd += float64(evidence/3)/1e6**m.PromptUSDPerMTok + float64(completion)/1e6**m.CompletionUSDPerMTok
	}
	return fmt.Sprintf("Estimated cost per review: at most ~$%.4f (an estimate from catalog prices: %d call(s), each with the full\n"+
		"%d-character evidence budget and %d completion tokens; reviews of small changes cost much less).\n"+
		"Cap it with [ai.budget] max_review_usd or `pistos review --max-ai-usd`.", usd, len(aliases), evidence, completion)
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ")
}

func (a *app) chooseProvider(ctx context.Context, p *prompter) (*providerChoice, error) {
	out := a.stdout
	fmt.Fprintln(out, "\nProvider:")
	fmt.Fprintln(out, "  1) OpenAI-compatible endpoint (OpenAI, Azure/AI gateways, vLLM, llama.cpp, Ollama, LM Studio, …)")
	fmt.Fprintln(out, "  2) OpenRouter (one key for hundreds of models from many vendors, with prices; mixing vendors")
	fmt.Fprintln(out, "     across passes finds more distinct issues than repeating one model)")
	n, err := p.number("Choose", 1, 1, 2)
	if err != nil {
		return nil, err
	}
	pc := &providerChoice{openRouter: n == 2}
	if pc.openRouter {
		pc.endpoint = openRouterEndpoint
		fmt.Fprintf(out, "endpoint %s\n", pc.endpoint)
	} else if pc.endpoint, err = a.askEndpoint(ctx, p); err != nil {
		return nil, err
	}
	if !pc.openRouter && ai.IsOpenRouter(pc.endpoint) {
		pc.openRouter = true
	}
	if err := a.askKey(ctx, p, pc); err != nil {
		return nil, err
	}
	return pc, nil
}

// normalizeEndpoint accepts what people paste: a missing scheme, a trailing slash or the full
// chat completions URL.
func normalizeEndpoint(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		host := s
		if i := strings.IndexByte(host, '/'); i >= 0 {
			host = host[:i]
		}
		if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
			s = "http://" + s
		} else {
			s = "https://" + s
		}
	}
	s = strings.TrimRight(s, "/")
	return strings.TrimSuffix(s, "/chat/completions")
}

func (a *app) askEndpoint(ctx context.Context, p *prompter) (string, error) {
	out := a.stdout
	fmt.Fprintln(out, "\nThe base URL that serves POST …/chat/completions, for example")
	fmt.Fprintln(out, "  https://api.openai.com/v1   http://localhost:11434/v1 (Ollama)   http://localhost:8000/v1 (vLLM)")
	for {
		raw, err := p.line("Endpoint", "")
		if err != nil {
			return "", err
		}
		if raw == "" {
			fmt.Fprintln(out, "  an endpoint is required")
			continue
		}
		ep := normalizeEndpoint(raw)
		if u, err := url.Parse(ep); err != nil || u.Host == "" {
			fmt.Fprintf(out, "  %q is not a URL\n", raw)
			continue
		}
		if err := config.ValidateEndpoint(ep); err != nil {
			fmt.Fprintf(out, "  %v\n", err)
			continue
		}
		if ep != raw {
			fmt.Fprintf(out, "  using %s\n", ep)
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = ai.Reachable(cctx, ep)
		cancel()
		if err == nil {
			return ep, nil
		}
		fmt.Fprintf(out, "  cannot reach %s: %v\n", ep, err)
		if ok, err := p.yes("Use it anyway (for example, the server is not running yet)?", false); err != nil || ok {
			return ep, err
		}
	}
}

func isLocal(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme == "http"
}

func defaultKeyEnv(pc *providerChoice) string {
	switch {
	case pc.openRouter:
		return "OPENROUTER_API_KEY"
	case strings.Contains(pc.endpoint, "api.openai.com"):
		return "OPENAI_API_KEY"
	}
	return "PISTOS_AI_KEY"
}

func (a *app) askKey(ctx context.Context, p *prompter, pc *providerChoice) error {
	out := a.stdout
	envName := defaultKeyEnv(pc)
	if v, src := ai.LookupKey(envName); v != "" {
		where := "$" + envName
		if src != "environment" {
			where = "the key saved as " + envName
		}
		use, err := p.yes(fmt.Sprintf("\nUse %s?", where), true)
		if err != nil {
			return err
		}
		if use {
			pc.key, pc.keyEnv = v, envName
			if err := a.verifyKey(ctx, pc); err == nil {
				return nil
			} else if !errors.Is(err, ai.ErrUnauthorized) {
				return nil
			}
			fmt.Fprintln(out, "  that key was rejected; enter another one")
			pc.key = ""
		}
	}
	fmt.Fprintln(out)
	for attempt := 1; ; attempt++ {
		q := "API key (input hidden)"
		if p.tty == nil {
			q = "API key"
		}
		if isLocal(pc.endpoint) {
			q += "; leave empty if the server needs none"
		}
		key, err := p.secret(q)
		if err != nil {
			return err
		}
		if key == "" && !isLocal(pc.endpoint) {
			if ok, err := p.yes("  No key: continue without authentication?", false); err != nil {
				return err
			} else if !ok {
				continue
			}
		}
		if strings.ContainsAny(key, " \t") {
			fmt.Fprintln(out, "  a key has no spaces; paste it again")
			continue
		}
		pc.key = key
		err = a.verifyKey(ctx, pc)
		switch {
		case err == nil:
		case errors.Is(err, ai.ErrUnauthorized):
			if attempt >= keyAttempts {
				return fmt.Errorf("%v after %d attempts; nothing was written", err, attempt)
			}
			fmt.Fprintf(out, "  %v; try again\n", err)
			continue
		default:
			fmt.Fprintf(out, "  could not verify the key: %v\n", err)
			if ok, err := p.yes("  Keep it anyway?", false); err != nil {
				return err
			} else if !ok {
				continue
			}
		}
		break
	}
	if pc.key == "" {
		return nil
	}
	name, err := p.line("Environment variable that holds the key (config.toml stores only this name)", envName)
	if err != nil {
		return err
	}
	for !regexpEnv(name) {
		fmt.Fprintln(out, "  use upper-case letters, digits and underscores, starting with a letter or underscore")
		if name, err = p.line("Environment variable", envName); err != nil {
			return err
		}
	}
	pc.keyEnv = name
	if os.Getenv(name) == pc.key {
		fmt.Fprintf(out, "  $%s already holds this key\n", name)
		return nil
	}
	cp, _ := ai.CredentialsPath()
	pc.saveKey, err = p.yes(fmt.Sprintf("Save the key for your user in %s (readable only by you; $%s still takes precedence)?", cp, name), true)
	return err
}

func regexpEnv(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// verifyKey checks the key and, on success, loads the model list used for picking.
func (a *app) verifyKey(ctx context.Context, pc *providerChoice) error {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := ai.CheckKey(cctx, pc.endpoint, pc.key, pc.openRouter)
	if err != nil && !errors.Is(err, ai.ErrNoModelList) {
		return err
	}
	if err == nil {
		fmt.Fprintln(a.stdout, "  ✓ key accepted")
	}
	models, lerr := ai.ListModels(cctx, pc.endpoint, pc.key)
	if lerr == nil {
		pc.models = models
		return nil
	}
	if err != nil {
		return err
	}
	return nil
}

func (a *app) chooseModels(p *prompter, pc *providerChoice) (config.AISetup, error) {
	out := a.stdout
	models := pc.models
	if pc.openRouter && len(models) > 0 {
		var usable []ai.ModelInfo
		for _, m := range models {
			if m.JSONMode {
				usable = append(usable, m)
			}
		}
		fmt.Fprintf(out, "\n%d of %d OpenRouter models support the JSON output pistos requests.\n", len(usable), len(models))
		models = usable
	} else if len(models) > 0 {
		fmt.Fprintf(out, "\nThe endpoint lists %d model(s).\n", len(models))
	} else {
		fmt.Fprintln(out, "\nThe endpoint does not list its models; type model ids as the provider documents them.")
	}

	fmt.Fprintln(out, "Each review can run several models independently; their findings are combined, duplicates merged,")
	fmt.Fprintln(out, "and a triage step rejects what it can refute. Three passes is the recommended balance of recall and cost.")
	n, err := p.number("How many models should review each change?", defaultPasses, 1, maxPasses)
	if err != nil {
		return config.AISetup{}, err
	}
	if pc.openRouter && n > 1 {
		fmt.Fprintln(out, "Tip: picking models from different vendors gives more independent opinions than repeating one.")
	}
	var setup config.AISetup
	aliases := map[string]string{}
	alias := func(modelID string) string {
		if a, ok := aliases[modelID]; ok {
			return a
		}
		name := config.ModelAliasName(modelID)
		for i := 2; ; i++ {
			taken := false
			for _, m := range setup.Models {
				taken = taken || m.Alias == name
			}
			if !taken {
				break
			}
			name = fmt.Sprintf("%s-%d", config.ModelAliasName(modelID), i)
		}
		aliases[modelID] = name
		conc := 2
		if pc.openRouter {
			conc = 4
		}
		m := config.ModelAlias{
			Backend: "openai-compatible", Endpoint: pc.endpoint, Model: modelID, APIKeyEnv: pc.keyEnv,
			Timeout: config.Duration{Duration: 2 * time.Minute}, Concurrency: conc,
		}
		// Catalog prices make spend estimates and caps possible; unknown (negative) prices are omitted.
		for _, info := range pc.models {
			if info.ID == modelID && info.PromptPrice >= 0 && info.CompletionPrice >= 0 {
				prompt, completion := info.PromptPrice, info.CompletionPrice
				m.PromptUSDPerMTok, m.CompletionUSDPerMTok = &prompt, &completion
			}
		}
		setup.Models = append(setup.Models, config.NamedModel{Alias: name, ModelAlias: m})
		return name
	}
	prev := ""
	var first string
	for i := 1; i <= n; i++ {
		id, err := a.pickModel(p, models, fmt.Sprintf("Model for pass %d of %d", i, n), prev)
		if err != nil {
			return config.AISetup{}, err
		}
		if i == 1 {
			first = id
		}
		prev = id
		setup.Passes = append(setup.Passes, config.AIPass{ID: fmt.Sprintf("pass-%d", i), Model: alias(id)})
	}

	fmt.Fprintln(out, "\nTriage re-checks every candidate before it is published:")
	fmt.Fprintln(out, "  1) a model tries to refute each candidate (one extra call per review)")
	fmt.Fprintln(out, "  2) evidence only: keep well-formed candidates, no extra call")
	def := 1
	if n == 1 {
		def = 2
	}
	t, err := p.number("Choose", def, 1, 2)
	if err != nil {
		return config.AISetup{}, err
	}
	if t == 2 {
		setup.Triage = config.AITriage{Mode: "evidence"}
		return setup, nil
	}
	id, err := a.pickModel(p, models, "Triage model", first)
	if err != nil {
		return config.AISetup{}, err
	}
	setup.Triage = config.AITriage{Mode: "model", Model: alias(id)}
	return setup, nil
}

const maxListed = 15

// pickModel lets a person search the catalog and pick by number, or type an exact id.
func (a *app) pickModel(p *prompter, models []ai.ModelInfo, label, def string) (string, error) {
	out := a.stdout
	var shown []ai.ModelInfo
	hint := " (type part of a name to search, a number from the list, or an exact id)"
	if len(models) == 0 {
		hint = " (exact model id)"
	}
	for {
		q, err := p.line(label+hint, def)
		if err != nil {
			return "", err
		}
		if q == "" {
			fmt.Fprintln(out, "  a model is required")
			continue
		}
		if n, err := strconv.Atoi(q); err == nil && n >= 1 && n <= len(shown) {
			fmt.Fprintf(out, "  → %s\n", shown[n-1].ID)
			return shown[n-1].ID, nil
		}
		if len(models) == 0 {
			return q, nil
		}
		var matches []ai.ModelInfo
		lq := strings.ToLower(q)
		for _, m := range models {
			if m.ID == q {
				return q, nil
			}
			if strings.Contains(strings.ToLower(m.ID), lq) || strings.Contains(strings.ToLower(m.Name), lq) {
				matches = append(matches, m)
			}
		}
		switch len(matches) {
		case 0:
			fmt.Fprintf(out, "  no listed model matches %q\n", q)
			if ok, err := p.yes(fmt.Sprintf("  Use %q as the model id anyway?", q), false); err != nil || ok {
				return q, err
			}
			continue
		case 1:
			fmt.Fprintf(out, "  → %s\n", matches[0].ID)
			return matches[0].ID, nil
		}
		shown = matches
		if len(shown) > maxListed {
			shown = shown[:maxListed]
		}
		for i, m := range shown {
			fmt.Fprintf(out, "  %2d) %s%s\n", i+1, m.ID, describeModel(m))
		}
		if len(matches) > len(shown) {
			fmt.Fprintf(out, "      … %d more; refine the search\n", len(matches)-len(shown))
		}
		def = ""
	}
}

func describeModel(m ai.ModelInfo) string {
	var parts []string
	if m.ContextLength > 0 {
		parts = append(parts, fmt.Sprintf("%dk context", m.ContextLength/1000))
	}
	if m.PromptPrice >= 0 && m.CompletionPrice >= 0 {
		parts = append(parts, fmt.Sprintf("$%.2f in / $%.2f out per 1M tokens", m.PromptPrice, m.CompletionPrice))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  · " + strings.Join(parts, " · ")
}
