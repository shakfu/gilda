// Package app wires a session together: it resolves the provider and model from flags and saved
// state, builds the agent, and switches provider or model mid-session. The headless runner and
// the REPL both drive an App.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/mock"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/price"
	"github.com/shakfu/gilda/prompt"
	"github.com/shakfu/gilda/provider"
	"github.com/shakfu/gilda/state"
	"github.com/shakfu/gilda/tool"
)

type Options struct {
	Provider string
	Model    string
	BaseURL  string
	// APIKey goes with Provider and BaseURL, for a gateway. Keys holds vendor keys by provider
	// id, such as {"anthropic": "sk-..."}; they win over the environment, which a GUI app
	// launched from the desktop does not inherit.
	APIKey    string
	Keys      map[string]string
	Effort    string
	MaxTokens int64
	MaxTurns  int
	Context   int64
	Mock      string
	Root      string
	// Refresh refetches the price list, ignoring its daily cache.
	Refresh bool
	// StateDir, CacheDir and ConfigDir replace gilda's XDG directories when set: saved state and
	// history, the price list, and the user's AGENTS.md and skills. An embedding app sets them
	// so it does not share the CLI's.
	StateDir, CacheDir, ConfigDir string
	// Permissions is auto, ask, all or read-only; empty takes the mode in settings.toml, then
	// auto. Ask is how a call that needs approval asks; nil refuses those calls. See the
	// permission package.
	Permissions string
	Ask         permission.AskFunc
	// Tools are added to read, write, edit and bash. Each declares its effect to the
	// permission modes through tool.ReadOnly and tool.Paths; tool.New builds one from
	// functions. Names must be unique.
	Tools []tool.Tool
	// Only, when set, keeps just the named tools, built-in or custom.
	Only []string
	// AppendSystem is added to the end of the system prompt.
	AppendSystem string
	// Continue resumes the newest session saved for Root; Resume resumes the session with that
	// id, or a unique prefix of it. Either takes the session's provider and model unless
	// Provider or Model is set.
	Continue bool
	Resume   string
	// Rules add secret and protected paths to the built-in ones and to those in the config
	// directory's settings.toml. They form their own layer: a "!" here exempts only patterns
	// given here.
	Rules permission.Rules
}

type App struct {
	Agent *agent.Agent
	// ProviderID names the registry entry, or "mock".
	ProviderID string
	Jobs       *tool.Jobs
	// Seen records what read returned, for edit and write; see tool.Seen.
	Seen  *tool.Seen
	State state.State

	opts     Options
	fixedCtx bool
	mode     permission.Mode
	// trustDir is the checkout whose AGENTS.md files would instruct auto mode when the mode is
	// not explicit; see Trust. distrusted is set when the user declined it in an earlier run.
	trustDir   string
	distrusted bool
	ask        permission.AskFunc
	rules      []permission.Rules
	diff       bool
	// fetchPrices is false when settings.toml turns off the price list.
	fetchPrices bool
	// saveSessions is false when settings.toml sets session.save = false. created is when the
	// current session was first saved; zero until then.
	saveSessions bool
	created      time.Time
	// saved is the Updated time of the session file as this App last wrote or read it.
	saved time.Time
	// warns are reported by Prepare.
	warns []error
	// applyPatch is tools.apply_patch from settings.toml; nil decides by provider.
	applyPatch *bool
	// task is tools.task from settings.toml. env is what the built-in tools share.
	task bool
	env  tool.Env
	// promptOpts says what the system prompt includes, for the trust question.
	promptOpts prompt.Options
	notify     string

	mu     sync.Mutex
	prices *price.Catalog
	models map[string][]llm.Model
	// remembered is set once a turn has streamed with the current provider and model.
	remembered bool
}

// New resolves the provider and model and builds the agent. It does no network I/O; call
// Prepare for the price list and context window.
func New(opts Options) (*App, error) {
	if opts.Root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		opts.Root = wd
	}
	if abs, err := filepath.Abs(opts.Root); err == nil {
		opts.Root = abs
	}
	for id := range opts.Keys {
		if _, ok := provider.Find(id); !ok {
			return nil, fmt.Errorf("key given for unknown provider %q", id)
		}
	}
	if p, m, ok := SplitModel(opts.Model); ok {
		if opts.Provider != "" && opts.Provider != p {
			return nil, fmt.Errorf("model %q names provider %s but --provider is %s", opts.Model, p, opts.Provider)
		}
		opts.Provider, opts.Model = p, m
	}
	// Neither a key nor an endpoint names its vendor, so autoselect could send the key to the
	// wrong one, or pick a provider whose endpoint is then ignored.
	switch {
	case opts.APIKey != "" && opts.Provider == "":
		return nil, fmt.Errorf("--api-key needs --provider")
	case opts.BaseURL != "" && opts.Provider == "":
		return nil, fmt.Errorf("--base-url needs --provider")
	}

	if opts.ConfigDir == "" {
		opts.ConfigDir = state.ConfigDir()
	}
	settings, err := state.LoadSettings(opts.ConfigDir)
	if err != nil {
		return nil, err
	}
	// An explicit mode wins over the settings file, which wins over the default.
	explicit := opts.Permissions != ""
	if opts.Permissions == "" {
		opts.Permissions = string(settings.Permissions.Mode)
	}
	mode, err := permission.Parse(opts.Permissions)
	if err != nil {
		return nil, err
	}
	// Separate layers: a "!" in the app's rules cannot lift one the user's settings added.
	if err := opts.Rules.Validate(); err != nil {
		return nil, err
	}
	rules := []permission.Rules{settings.Permissions.Rules, opts.Rules}
	diff := state.Or(settings.Permissions.Diff, true)
	// Flags win over settings.toml, which wins over the agent's defaults.
	sa := settings.Agent
	if opts.MaxTokens == 0 {
		opts.MaxTokens = state.Or(sa.MaxTokens, 0)
	}
	if opts.MaxTurns == 0 {
		opts.MaxTurns = state.Or(sa.MaxTurns, 0)
	}
	if opts.Context == 0 {
		opts.Context = state.Or(sa.Context, 0)
	}
	// agent.Config reads 0 as the default and a negative number as none.
	retries := state.Or(sa.StreamRetries, 0)
	if sa.StreamRetries != nil && retries == 0 {
		retries = -1
	}
	st, d := settings.Tools, tool.DefaultLimits
	limits := tool.Limits{
		OutputCap:      state.Or(st.OutputCap, d.OutputCap),
		ReadLines:      state.Or(st.ReadLines, d.ReadLines),
		ReadLineBytes:  state.Or(st.ReadLineBytes, d.ReadLineBytes),
		BashTimeout:    state.Or(st.BashTimeout, d.BashTimeout),
		BashMaxTimeout: state.Or(st.BashMaxTimeout, d.BashMaxTimeout),
		DiffBytes:      state.Or(settings.Permissions.DiffMaxBytes, d.DiffBytes),
	}
	if limits.BashTimeout > limits.BashMaxTimeout {
		return nil, fmt.Errorf("settings.toml: tools.bash_timeout %d exceeds tools.bash_max_timeout %d",
			limits.BashTimeout, limits.BashMaxTimeout)
	}
	promptOpts := prompt.Options{
		NoAgents: !state.Or(settings.Prompt.AgentsMD, true),
		NoSkills: !state.Or(settings.Prompt.Skills, true),
	}
	fetchPrices := state.Or(settings.Prices.Fetch, true)
	if opts.CacheDir == "" {
		opts.CacheDir = state.CacheDir()
	}
	saved := state.Load(opts.StateDir)
	var resumed *session
	if opts.Continue || opts.Resume != "" {
		if opts.Continue && opts.Resume != "" {
			return nil, fmt.Errorf("--continue and --resume are exclusive")
		}
		if resumed, err = findSession(saved.Dir(), opts.Root, opts.Resume); err != nil {
			return nil, err
		}
		if opts.Provider == "" && opts.Model == "" && opts.Mock == "" {
			opts.Provider, opts.Model = resumed.Provider, resumed.Model
		}
	}
	a := &App{opts: opts, State: saved, Jobs: &tool.Jobs{}, Seen: &tool.Seen{}, models: map[string][]llm.Model{}, diff: diff,
		fetchPrices: fetchPrices, saveSessions: state.Or(settings.Session.Save, true)}
	if opts.Effort == "" {
		opts.Effort = a.State.Effort
	}
	env := tool.Env{Root: opts.Root, Jobs: a.Jobs, Limits: limits, Hide: HiddenEnv(st.BashEnv), Seen: a.Seen}
	a.applyPatch, a.env, a.task = st.ApplyPatch, env, state.Or(st.Task, true)
	a.notify = settings.REPL.Notify
	if a.notify == "" {
		a.notify = "bell"
	}
	cfg := agent.Config{
		System:        withAppended(prompt.Build(opts.Root, opts.ConfigDir, promptOpts), opts.AppendSystem),
		Tools:         append(tool.Default(env), opts.Tools...),
		MaxTokens:     opts.MaxTokens,
		MaxTurns:      opts.MaxTurns,
		StreamRetries: retries,
		OutputCap:     limits.OutputCap,
		Effort:        opts.Effort,
		Context:       opts.Context,
		SessionID:     newSessionID(),
	}
	if err := tool.Check(cfg.Tools); err != nil {
		return nil, err
	}
	a.fixedCtx = opts.Context > 0
	a.promptOpts = promptOpts
	if !explicit && mode == permission.Auto {
		if dir, files := checkout(opts.Root, promptOpts); len(files) > 0 {
			trusted, answered := a.State.Trust[dir]
			switch {
			case !answered:
				a.trustDir = dir
			case !trusted:
				mode, a.distrusted = permission.Ask, true
			}
		}
	}
	a.mode, a.ask, a.rules = mode, opts.Ask, rules
	cfg.Approve = permission.Approver(mode, opts.Root, opts.Ask, rules...)

	if opts.Mock != "" {
		p, err := mock.Load(opts.Mock)
		if err != nil {
			return nil, err
		}
		cfg.Provider, cfg.Model, a.ProviderID = p, "mock", "mock"
		a.Agent = agent.New(cfg)
		if err := a.addPatch(env); err != nil {
			return nil, err
		}
		if err := a.addTask(); err != nil {
			return nil, err
		}
		if err := a.keepOnly(opts.Only); err != nil {
			return nil, err
		}
		a.resume(resumed)
		return a, nil
	}

	id := opts.Provider
	if id == "" {
		var err error
		if id, err = provider.Choose(a.State.Provider, opts.Keys); err != nil {
			return nil, err
		}
	}
	key, base := a.credentials(id)
	p, err := provider.Open(id, key, base)
	if err != nil {
		return nil, err
	}
	cfg.Provider = p
	a.ProviderID = id
	a.Agent = agent.New(cfg)
	a.Agent.Model = a.defaultModel(id, opts.Model)
	if err := a.addPatch(env); err != nil {
		return nil, err
	}
	if err := a.addTask(); err != nil {
		return nil, err
	}
	if err := a.keepOnly(opts.Only); err != nil {
		return nil, err
	}
	a.resume(resumed)
	return a, nil
}

// keepOnly drops every tool not named; no names keeps all. An editing tool without read is
// refused, since it changes only files read has returned.
func (a *App) keepOnly(names []string) error {
	if len(names) == 0 {
		return nil
	}
	var kept []tool.Tool
	for _, n := range names {
		t := tool.Find(a.Agent.Tools, n)
		if t == nil {
			var all []string
			for _, t := range a.Agent.Tools {
				all = append(all, t.Spec().Name)
			}
			return fmt.Errorf("--tools: no tool %q; the tools are %s", n, strings.Join(all, ", "))
		}
		if tool.Find(kept, n) == nil {
			kept = append(kept, t)
		}
	}
	if tool.Find(kept, "read") == nil {
		for _, n := range []string{"edit", "apply_patch"} {
			if tool.Find(kept, n) != nil {
				return fmt.Errorf("--tools: %s needs read, since it changes only files the model has read", n)
			}
		}
	}
	a.Agent.Tools = kept
	return nil
}

// withAppended adds the caller's instructions after gilda's system prompt.
func withAppended(system, extra string) string {
	if strings.TrimSpace(extra) == "" {
		return system
	}
	return system + "\n# Additional instructions\n\n" + strings.TrimSpace(extra) + "\n"
}

// addPatch offers apply_patch after the built-in tools when settings.toml says so, or by
// default to an OpenAI model, which is trained on it. The choice holds for the session, since
// changing the tool list mid-session would drop the prompt cache.
func (a *App) addPatch(env tool.Env) error {
	on := a.ProviderID == "openai" || a.ProviderID == "openrouter" && strings.HasPrefix(a.Agent.Model, "openai/")
	if a.applyPatch != nil {
		on = *a.applyPatch
	}
	if !on {
		return nil
	}
	a.insertTool(tool.Patch{Env: env})
	return tool.Check(a.Agent.Tools)
}

// insertTool puts t after the built-in tools added so far, before the app's own.
func (a *App) insertTool(t tool.Tool) {
	n := len(a.Agent.Tools) - len(a.opts.Tools)
	tools := append(slices.Clone(a.Agent.Tools[:n]), t)
	a.Agent.Tools = append(tools, a.Agent.Tools[n:]...)
}

// addTask offers the task tool unless settings.toml turns it off.
func (a *App) addTask() error {
	if !a.task {
		return nil
	}
	a.insertTool(agent.Task{New: a.subagent})
	return tool.Check(a.Agent.Tools)
}

// subagent builds the agent one task call runs: the caller's provider, model, limits and
// approvals as they are now, read and bash only, and a read record of its own, so the caller
// cannot edit a file only the subagent has read.
func (a *App) subagent() *agent.Agent {
	env := a.env
	env.Seen = &tool.Seen{}
	cfg := a.Agent.Config
	cfg.System = a.Agent.System + "\n" + agent.TaskPrompt + "\n"
	cfg.Tools = []tool.Tool{tool.Read{Env: env}, tool.Bash{Env: env}}
	cfg.SessionID = a.Agent.SessionID + "-task"
	return agent.New(cfg)
}

// resume loads a saved session into the agent; nil does nothing.
func (a *App) resume(s *session) {
	if s == nil {
		return
	}
	history, dropped := s.restore()
	a.Agent.History, a.Agent.Used, a.Agent.SessionID, a.created, a.saved = history, s.Used, s.ID, s.Created, s.Updated
	if dropped > 0 {
		a.warns = append(a.warns, fmt.Errorf("session %s: reasoning of %d messages could not be restored; they resume as text", s.ID, dropped))
	}
}

// Reset starts a new conversation, saved as a new session. Session usage is kept.
func (a *App) Reset() {
	a.Agent.Reset()
	a.Seen.Reset()
	a.Agent.SessionID = newSessionID()
	a.created, a.saved = time.Time{}, time.Time{}
}

// Prepare loads the price list and resolves the context window and, for a local server, the
// model. It returns an error when the provider lists models and the chosen one is not among
// them. Other failures cost only estimates, so they come back as warnings.
func (a *App) Prepare(ctx context.Context) ([]error, error) {
	warns := a.warns
	a.warns = nil
	if a.ProviderID == "mock" {
		return warns, nil
	}
	e, _ := provider.Find(a.ProviderID)
	if a.Agent.Model == "" {
		models, err := a.Models(ctx)
		if err != nil || len(models) == 0 {
			return warns, fmt.Errorf("no model given and %s lists none: %v", a.ProviderID, err)
		}
		a.Agent.Model = models[0].ID
	}
	if err := a.checkModel(ctx, a.ProviderID, a.Agent.Provider, a.Agent.Model); err != nil {
		return warns, err
	}
	// The price list serves every cloud provider a session may switch to. A gateway need not bill
	// at the vendor's rates, and a local server bills nothing.
	if a.fetchPrices && (!e.Local() || a.opts.BaseURL == "") {
		cat, err := price.Load(ctx, a.opts.CacheDir, a.opts.Refresh)
		if err != nil {
			warns = append(warns, fmt.Errorf("price list: %w", err))
		}
		a.mu.Lock()
		a.prices = cat
		a.mu.Unlock()
		if !e.Local() && a.ProviderID != "openrouter" && a.usesVendorURL(a.ProviderID) {
			a.Agent.Prices = cat
		}
	}
	a.resolveContext(ctx)
	return warns, nil
}

// resolveContext sets the window from the provider's listing, then OpenRouter's, and whether
// the model accepts images.
func (a *App) resolveContext(ctx context.Context) {
	a.Agent.Images = a.acceptsImages()
	if a.fixedCtx {
		return
	}
	a.Agent.Context = 0
	if models, err := a.Models(ctx); err == nil {
		for _, m := range models {
			if m.ID == a.Agent.Model && m.Context > 0 {
				a.Agent.Context = m.Context
				return
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.prices.Lookup(a.ProviderID, a.Agent.Model); ok {
		a.Agent.Context = e.Context
	}
}

// acceptsImages reports whether the current model takes images, from OpenRouter's list. A model
// the list does not name is taken to accept them on the vendor APIs, whose chat models do, and
// not elsewhere: a local server or an unknown OpenRouter model would reject every later request.
func (a *App) acceptsImages() bool {
	a.mu.Lock()
	e, listed := a.prices.Lookup(a.ProviderID, a.Agent.Model)
	a.mu.Unlock()
	if ok, known := e.Images(); listed && known {
		return ok
	}
	return a.ProviderID == "anthropic" || a.ProviderID == "openai"
}

// Models lists the current provider's models, sorted by id, cached for the session.
func (a *App) Models(ctx context.Context) ([]llm.Model, error) {
	return a.modelsFor(ctx, a.ProviderID, a.Agent.Provider)
}

func (a *App) modelsFor(ctx context.Context, id string, p llm.Provider) ([]llm.Model, error) {
	a.mu.Lock()
	cached, ok := a.models[id]
	a.mu.Unlock()
	if ok {
		return cached, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	models, err := p.Models(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	a.mu.Lock()
	a.models[id] = models
	a.mu.Unlock()
	return models, nil
}

// checkModel refuses a model the provider does not list, so a model meant for another
// provider fails here with a hint rather than on the first request. A provider that lists
// nothing, or cannot be reached, is given the benefit of the doubt.
func (a *App) checkModel(ctx context.Context, id string, p llm.Provider, model string) error {
	models, err := a.modelsFor(ctx, id, p)
	if err != nil || len(models) == 0 {
		return nil
	}
	for _, m := range models {
		if m.ID == model {
			return nil
		}
	}
	hint := "; /models in the REPL lists them"
	switch {
	case strings.Contains(model, "/") && !strings.HasPrefix(model, "/") && !isLocal(id) && id != "openrouter":
		hint = fmt.Sprintf("; for an OpenRouter model, use openrouter:%s", model)
	default:
		if near := nearModels(models, model); len(near) > 0 {
			hint = "; did you mean " + strings.Join(near, ", ") + "?"
		}
	}
	return fmt.Errorf("%s does not offer model %q%s", id, model, hint)
}

func isLocal(id string) bool {
	e, ok := provider.Find(id)
	return ok && e.Local()
}

// nearModels returns up to three listed ids that share the model's leading word, such as
// gpt-5.5-mini for gpt-5.5-mni.
func nearModels(models []llm.Model, model string) []string {
	stem, _, _ := strings.Cut(strings.ToLower(model), "-")
	var out []string
	for _, m := range models {
		if strings.HasPrefix(strings.ToLower(m.ID), stem) && len(out) < 3 {
			out = append(out, m.ID)
		}
	}
	return out
}

// Switch changes provider, model or both. An empty provider keeps the current one; an empty
// model takes the one last used with the provider, then its default, then the endpoint's first.
// Nothing changes unless the switch succeeds. History is kept: messages another model produced
// are replayed as text and tool calls.
func (a *App) Switch(ctx context.Context, providerID, model string) error {
	if p, m, ok := SplitModel(model); ok {
		providerID, model = p, m
	}
	p, id := a.Agent.Provider, a.ProviderID
	if providerID != "" && providerID != a.ProviderID {
		if a.ProviderID == "mock" {
			return fmt.Errorf("cannot switch provider in a mock session")
		}
		key, base := a.credentials(providerID)
		var err error
		if p, err = provider.Open(providerID, key, base); err != nil {
			return err
		}
		id = providerID
		model = a.defaultModel(id, model)
	}
	if model == "" && id == a.ProviderID {
		model = a.Agent.Model
	}
	if model == "" {
		models, err := a.modelsFor(ctx, id, p)
		if err != nil || len(models) == 0 {
			return fmt.Errorf("name a model: %s lists none (%v)", id, err)
		}
		model = models[0].ID
	}
	if err := a.checkModel(ctx, id, p, model); err != nil {
		return err
	}

	a.Agent.Provider, a.ProviderID, a.Agent.Model = p, id, model
	a.Agent.Prices = nil
	if e, ok := provider.Find(id); ok && !e.Local() && id != "openrouter" && a.usesVendorURL(id) {
		a.mu.Lock()
		a.Agent.Prices = a.prices
		a.mu.Unlock()
	}
	a.remembered = false
	a.resolveContext(ctx)
	return nil
}

// credentials returns the key and endpoint for a provider. APIKey and BaseURL belong together,
// so a gateway key never reaches the vendor's own endpoint; otherwise a key from Keys applies,
// and an empty one leaves the environment to the provider.
func (a *App) credentials(id string) (key, base string) {
	if id == a.opts.Provider && (a.opts.APIKey != "" || a.opts.BaseURL != "") {
		key, base = a.opts.APIKey, a.opts.BaseURL
		if key == "" {
			key = a.opts.Keys[id]
		}
		return key, base
	}
	return a.opts.Keys[id], ""
}

// HasKey reports whether a provider can run: a local server, or a key given or in the
// environment.
func (a *App) HasKey(id string) bool {
	e, ok := provider.Find(id)
	return ok && (e.Local() || a.opts.Keys[id] != "" || e.Key() != "")
}

// Notify is how the REPL gets attention after a long turn: bell, osc9 or off.
func (a *App) Notify() string { return a.notify }

// ConfigDir is where the user's AGENTS.md and skills are read from.
func (a *App) ConfigDir() string { return a.opts.ConfigDir }

// Root is the working directory the tools and the permission checks use.
func (a *App) Root() string { return a.opts.Root }

// usesVendorURL reports whether the provider talks to its vendor, whose rates the price list
// quotes, rather than a --base-url gateway.
func (a *App) usesVendorURL(id string) bool {
	return id != a.opts.Provider || a.opts.BaseURL == ""
}

// Preview returns what an approval shows below the call: a diff when the tool can make one and
// settings.toml does not set diff = false, else "". A failed preview shows nothing; the call fails the same way.
func (a *App) Preview(t tool.Tool, call llm.ToolCall) string {
	if !a.diff {
		return ""
	}
	p, ok := t.(tool.Previewer)
	if !ok {
		return ""
	}
	out, err := p.Preview(json.RawMessage(call.Arguments))
	if err != nil {
		return ""
	}
	return out
}

// SetEffort changes the reasoning effort and remembers it.
func (a *App) SetEffort(effort string) error {
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("effort must be low, medium, high, xhigh or max")
	}
	a.Agent.Effort = effort
	a.State.Effort = effort
	return a.State.Save()
}

// Remember saves the provider and model once a turn has streamed with them, so a mistyped
// model is never reused by a later run.
func (a *App) Remember() {
	if a.remembered || a.ProviderID == "mock" {
		return
	}
	a.remembered = true
	a.State.Provider = a.ProviderID
	a.State.Models[a.ProviderID] = a.Agent.Model
	_ = a.State.Save()
}

// Mode is the permission mode in force.
func (a *App) Mode() permission.Mode { return a.mode }

// SetPermissions changes the mode, the way calls that need approval ask, or both. An empty
// mode keeps the current one; a nil ask refuses those calls.
func (a *App) SetPermissions(mode permission.Mode, ask permission.AskFunc) {
	if mode != "" {
		a.mode = mode
	}
	a.ask = ask
	a.Agent.Approve = permission.Approver(a.mode, a.opts.Root, ask, a.rules...)
}

// Rules are the layers in force beyond the built-in one: the user's settings, then the app's.
func (a *App) Rules() permission.Layers { return a.rules }

// Ask is how calls that need approval currently ask.
func (a *App) Ask() permission.AskFunc { return a.ask }

// Close stops background jobs left by bash.
func (a *App) Close() { a.Jobs.Kill() }

func (a *App) defaultModel(id, model string) string {
	if model != "" {
		return model
	}
	if m := a.State.Models[id]; m != "" {
		return m
	}
	e, _ := provider.Find(id)
	return e.Model
}

// SplitModel reads "provider:model" when the prefix is a registry id, as in
// openrouter:openai/gpt-5.5 or ollama:qwen3:8b.
func SplitModel(s string) (string, string, bool) {
	p, m, ok := strings.Cut(s, ":")
	if !ok {
		return "", "", false
	}
	if _, known := provider.Find(p); !known {
		return "", "", false
	}
	return p, m, true
}

// checkout returns the repository holding root, or root outside one, and the files from it
// that the system prompt includes: AGENTS.md files and the repository's skills.
func checkout(root string, o prompt.Options) (string, []string) {
	dir, err := filepath.Abs(root)
	if err != nil {
		return "", nil
	}
	if r := prompt.RepoRoot(dir); r != "" {
		dir = r
	}
	var files []string
	if !o.NoAgents {
		files = prompt.AgentsFiles(root, "")
	}
	if !o.NoSkills {
		for _, s := range prompt.Skills(prompt.ProjectSkillsDir(root)) {
			files = append(files, s.Path)
		}
	}
	return dir, files
}

// Trust returns a directory whose AGENTS.md files and skills, listed, would instruct an agent that runs
// bash without asking, when the user has not yet said whether to trust it. It returns "" when
// no answer is needed: the mode was set explicitly, is not auto, or the directory was answered
// before. A cloned repository could otherwise direct the agent before the user has read it.
func (a *App) Trust() (string, []string) {
	if a.trustDir == "" {
		return "", nil
	}
	_, files := checkout(a.opts.Root, a.promptOpts)
	return a.trustDir, files
}

// SetTrust records the user's answer for dir. Declining switches to ask mode, now and in later
// runs that do not set a mode explicitly.
func (a *App) SetTrust(dir string, ok bool) error {
	a.trustDir = ""
	if !ok {
		a.distrusted = true
		a.SetPermissions(permission.Ask, a.ask)
	}
	if a.State.Trust == nil {
		a.State.Trust = map[string]bool{}
	}
	a.State.Trust[dir] = ok
	return a.State.Save()
}

// Distrusted reports whether the mode is ask because the user declined to trust the checkout.
func (a *App) Distrusted() bool { return a.distrusted }

// HiddenEnv lists the variables bash does not receive: every provider's key variables and
// GILDA_API_KEY, except those named in keep. A command could otherwise print a key into the
// history, from where it reaches the next provider.
func HiddenEnv(keep []string) []string {
	hide := []string{"GILDA_API_KEY"}
	for _, e := range provider.Registry {
		hide = append(hide, e.KeyEnv...)
	}
	return slices.DeleteFunc(hide, func(name string) bool { return slices.Contains(keep, name) })
}

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "gilda-" + hex.EncodeToString(b)
}
