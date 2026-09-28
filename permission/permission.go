// Package permission decides which tool calls run without asking. A Mode becomes an
// agent.Config.Approve function; an embedding app can use it or write its own.
//
// Decisions follow what a tool declares (see tool.ReadOnly and tool.Paths), not its name, so
// a custom tool cannot inherit a built-in's policy by reusing its name. The checks guard
// against mistakes, not an adversary: bash is not confined in any mode but read-only. A tool
// that is not a tool.Binder resolves its paths again when it runs, so a symlink swapped
// between the check and the write can redirect it; write and edit are Binders.
package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/tool"
)

type Mode string

const (
	// Auto runs read-only tools and bash, and a modifying tool whose paths are all inside the
	// working directory and outside .git and secret files. Everything else asks.
	Auto Mode = "auto"
	// Ask runs read-only tools and asks before everything else.
	Ask Mode = "ask"
	// All runs every call.
	All Mode = "all"
	// ReadOnly runs read-only tools and refuses everything else.
	ReadOnly Mode = "read-only"
)

// Modes lists the modes in the order help text shows them.
var Modes = []Mode{Auto, Ask, All, ReadOnly}

// Parse reads a mode name; empty means Auto.
func Parse(s string) (Mode, error) {
	if s == "" {
		return Auto, nil
	}
	for _, m := range Modes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", fmt.Errorf("permissions must be auto, ask, all or read-only, not %q", s)
}

// AskFunc asks the user whether a call may run. t is the tool that will run it, bound when it
// is a tool.Binder, so a preview should come from t. label is the call as the user sees it,
// such as "$ go test". why says what about the call needs approval.
type AskFunc func(ctx context.Context, t tool.Tool, call llm.ToolCall, label string, why Reason) (bool, error)

// Kind classifies why a call needs approval.
type Kind int

const (
	// General means the mode asks before the tool itself, as ask mode does.
	General Kind = iota
	// Outside means a path is outside the working directory.
	Outside
	// Undeclared means the tool declares no paths, so it may write anywhere.
	Undeclared
	// Host means a network tool contacts a host no layer allows.
	Host
	// Protected means a write under a protected path, such as .git.
	Protected
	// Secret means a read or write of a secret, such as .env.
	Secret
)

// Reason says why a call needs approval or was refused.
type Reason struct {
	Kind Kind
	// Text is for the user and the model, such as "read of .env, which may hold secrets".
	Text string
}

func (r Reason) String() string { return r.Text }

// Lasting reports whether an approval may be remembered for later calls of the same tool with
// the same Kind. Secrets and protected paths are approved one call at a time, since remembering
// one would lift a built-in rule.
func (r Reason) Lasting() bool { return r.Kind != Secret && r.Kind != Protected }

// Approver returns the approval function for mode, with root as the working directory. Each
// Rules is a layer, such as the user's settings and an embedding app's own, added to the
// built-in layer; see Layers. A call that would ask is refused when ask is nil, as in a run
// with no one to ask.
func Approver(mode Mode, root string, ask AskFunc, rules ...Rules) func(context.Context, tool.Tool, llm.ToolCall, string) (bool, error) {
	layers := Layers(rules)
	return func(ctx context.Context, t tool.Tool, call llm.ToolCall, label string) (bool, error) {
		v, why := layers.decide(mode, root, t, call)
		switch {
		case v == run:
			return true, nil
		case v == refuse:
			return false, fmt.Errorf("refused: %s in %s mode", why.Text, mode)
		case ask == nil:
			return false, fmt.Errorf("refused: %s mode asks before %s, and there is no one to ask", mode, why.Text)
		}
		return ask(ctx, t, call, label, why)
	}
}

type verdict int

const (
	run verdict = iota
	askUser
	refuse
)

// decide returns the verdict and, unless the call runs, what about it needs approval.
func (r Layers) decide(mode Mode, root string, t tool.Tool, call llm.ToolCall) (verdict, Reason) {
	if mode == All {
		return run, Reason{}
	}
	name := call.Name
	paths, perr := declared(t, call)
	// A network tool is judged by its hosts first, whatever ReadOnly says.
	if ht, ok := t.(tool.Hosts); ok {
		return r.network(mode, root, t, ht, call, paths, perr)
	}
	if ro, ok := t.(tool.ReadOnly); ok && ro.ReadOnly() {
		for _, p := range paths {
			if r.IsSecret(root, p) {
				why := secretReason(name, p)
				if mode == ReadOnly {
					return refuse, why
				}
				return askUser, why
			}
		}
		return run, Reason{}
	}
	_, isBash := t.(tool.Bash)
	switch mode {
	case ReadOnly:
		return refuse, Reason{General, name + ", which can change the environment,"}
	case Ask:
		if isBash && r.allows(call) {
			return run, Reason{}
		}
		return askUser, r.sensitive(root, name, paths, Reason{General, name})
	}
	// Auto. bash is recognised by its type, so a custom tool cannot take its policy by name.
	if isBash {
		return run, Reason{}
	}
	return r.writes(root, t, name, paths, perr)
}

// network judges a call by the hosts it contacts, then by the files it writes. A call to
// listed hosts that writes no files runs in every mode; one that writes files is judged as a
// modifying tool.
func (r Layers) network(mode Mode, root string, t tool.Tool, ht tool.Hosts, call llm.ToolCall, paths []string, perr error) (verdict, Reason) {
	name := call.Name
	hosts, err := ht.Hosts(json.RawMessage(call.Arguments))
	if err != nil {
		// The tool rejects invalid arguments; there is nothing to reach.
		return run, Reason{}
	}
	if host := r.unlisted(hosts); host != "" {
		why := Reason{Host, fmt.Sprintf("%s to %s, which is not in the hosts allowlist", name, host)}
		if mode == ReadOnly {
			return refuse, why
		}
		return askUser, why
	}
	if _, ok := t.(tool.Paths); ok && perr == nil && len(paths) == 0 {
		return run, Reason{}
	}
	switch mode {
	case ReadOnly:
		return refuse, Reason{General, name + ", which can write files,"}
	case Ask:
		return askUser, r.sensitive(root, name, paths, Reason{General, name})
	}
	return r.writes(root, t, name, paths, perr)
}

// writes judges a modifying call in auto mode by the paths it declares.
func (r Layers) writes(root string, t tool.Tool, name string, paths []string, perr error) (verdict, Reason) {
	if _, ok := t.(tool.Paths); !ok {
		return askUser, Reason{Undeclared, name + ", which declares no paths,"}
	}
	if perr != nil {
		// The tool rejects invalid arguments; there is nothing to confine.
		return run, Reason{}
	}
	outside := Reason{}
	for _, p := range paths {
		if !Inside(root, p) {
			outside = Reason{Outside, fmt.Sprintf("%s outside %s", name, root)}
			break
		}
	}
	// A secret or protected path outranks Outside, whose approval can be remembered.
	why := r.sensitive(root, name, paths, outside)
	if why.Text == "" {
		return run, Reason{}
	}
	return askUser, why
}

// sensitive returns the reason for the first secret or protected path a modifying call names,
// or def when there is none.
func (r Layers) sensitive(root, name string, paths []string, def Reason) Reason {
	for _, p := range paths {
		if r.IsSecret(root, p) {
			return secretReason(name, p)
		}
		if part := r.Protects(root, p); part != "" {
			return Reason{Protected, fmt.Sprintf("%s under %s", name, part)}
		}
	}
	return def
}

func secretReason(name, path string) Reason {
	return Reason{Secret, fmt.Sprintf("%s of %s, which may hold secrets", name, filepath.Base(path))}
}

// unlisted returns the first host no layer allows, or "".
func (r Layers) unlisted(hosts []string) string {
	for _, h := range hosts {
		ok := false
		for _, rules := range r {
			if hostAllowed(rules.Hosts, h) {
				ok = true
				break
			}
		}
		if !ok {
			return h
		}
	}
	return ""
}

// declared returns the paths a call names, when its tool declares them.
func declared(t tool.Tool, call llm.ToolCall) ([]string, error) {
	pt, ok := t.(tool.Paths)
	if !ok {
		return nil, nil
	}
	return pt.Paths(json.RawMessage(call.Arguments))
}

// Rules name the paths that need approval even where a mode would run the call. They only add
// to the built-in rules, which always apply, as one layer among others (see Layers). Each list holds glob patterns, as
// filepath.Match reads them:
//
//   - A pattern without a slash matches a file's name for Secrets, and any component of the
//     path under the root for Protected, so ".git" covers everything inside .git.
//   - A pattern with a slash matches the path relative to the root and everything under it,
//     so "config/prod" covers config/prod/db.yaml.
//   - A leading "!" exempts what an earlier pattern in the same Rules matched. The last
//     matching pattern wins, as in .gitignore. It cannot exempt a pattern in another layer,
//     built-in rules included.
//
// Paths are checked as written and after following symlinks; either matching counts. Case is
// ignored.
type Rules struct {
	// Secrets ask before any read or write, even by a read-only tool.
	Secrets []string `toml:"secrets"`
	// Protected ask before a write by a modifying tool.
	Protected []string `toml:"protected"`
	// Commands run through bash without asking in ask mode. Each is a word prefix: "go test"
	// allows "go test ./..." with any further arguments, so choose entries whose arguments
	// cannot run other programs. An entry ending in "$" must match the whole command:
	// "git status$" allows "git status" alone. A command that chains, substitutes or
	// redirects never matches. Unlike the path lists, commands loosen; any layer's entry
	// allows a command.
	Commands []string `toml:"commands"`
	// Hosts are the hosts a network tool may contact without asking; see tool.Hosts. An
	// entry is a host, such as "pkg.go.dev", or "*." and a domain for any host below it,
	// such as "*.githubusercontent.com". Like Commands, hosts loosen; any layer's entry
	// allows a host.
	Hosts []string `toml:"hosts"`
}

// builtin always applies, so it holds only names that nearly always mean credentials or
// unrebuildable history: a false positive here asks forever and cannot be lifted.
var builtin = Rules{
	Secrets: []string{
		// dotenv files, except committed templates; .envrc does not match.
		".env", ".env.*", "!.env.*example*", "!.env.*sample*", "!.env.*template*",
		// SSH private keys; the .pub halves do not match.
		"id_rsa", "id_dsa", "id_ecdsa", "id_ecdsa_sk", "id_ed25519", "id_ed25519_sk",
		// Private keys and key stores.
		"*.key", "*.p12", "*.pfx", "*.jks", "*.keystore", "*.kdbx",
		// Credential files: git, curl and ftp, PostgreSQL, PyPI, Apache, AWS, Google.
		".git-credentials", ".netrc", "_netrc", ".pgpass", ".pypirc", ".htpasswd",
		"credentials", "credentials.json",
		// Terraform state stores resource secrets in plain text.
		"*.tfstate", "*.tfstate.backup",
	},
	// Version-control metadata: its loss is history no later turn can rebuild.
	Protected: []string{".git", ".hg", ".svn", ".jj"},
}

// Builtin returns the rules that always apply.
func Builtin() Rules {
	return Rules{Secrets: slices.Clone(builtin.Secrets), Protected: slices.Clone(builtin.Protected)}
}

// Validate reports the first malformed pattern or command.
func (r Rules) Validate() error {
	for _, c := range r.Commands {
		if err := checkCommand(c); err != nil {
			return err
		}
	}
	for _, h := range r.Hosts {
		if err := checkHost(h); err != nil {
			return err
		}
	}
	for _, list := range [][]string{r.Secrets, r.Protected} {
		for _, p := range list {
			bare := strings.TrimPrefix(p, "!")
			if _, err := filepath.Match(bare, ""); err != nil || bare == "" {
				return fmt.Errorf("bad path pattern %q", p)
			}
		}
	}
	return nil
}

// Layers are rule sets from separate sources, checked on top of the built-in rules. A path
// needs approval when any layer matches it. A "!" pattern exempts only within its own layer,
// so no source can lift a protection another one added.
type Layers []Rules

// allows reports whether any layer's Commands allows a bash call.
func (r Layers) allows(call llm.ToolCall) bool {
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(call.Arguments), &args) != nil {
		return false
	}
	for _, rules := range r {
		if allowed(rules.Commands, args.Command) {
			return true
		}
	}
	return false
}

// all returns the built-in layer followed by the given ones.
func (r Layers) all() []Rules { return append([]Rules{builtin}, r...) }

// IsSecret reports whether path, taken relative to root, is a secret in any layer.
func (r Layers) IsSecret(root, path string) bool {
	for _, rules := range r.all() {
		if rules.secret(root, path) {
			return true
		}
	}
	return false
}

// Protects returns the part of a path that a modifying tool should not write without asking,
// in any layer: a protected component or a secret's name. It returns "" for anything else.
func (r Layers) Protects(root, path string) string {
	for _, rules := range r.all() {
		for _, p := range candidates(root, path) {
			if match(rules.Protected, p.components, p.prefixes) {
				return protectedPart(rules.Protected, p)
			}
		}
	}
	if r.IsSecret(root, path) {
		return filepath.Base(path)
	}
	return ""
}

func (r Rules) secret(root, path string) bool {
	for _, p := range candidates(root, path) {
		if match(r.Secrets, []string{filepath.Base(p.abs)}, p.prefixes) {
			return true
		}
	}
	return false
}

// protectedPart names what matched, for the message the model gets.
func protectedPart(patterns []string, p candidate) string {
	for _, c := range append(append([]string(nil), p.components...), p.prefixes...) {
		if match(patterns, []string{c}, []string{c}) {
			return c
		}
	}
	return filepath.Base(p.abs)
}

type candidate struct {
	abs string
	// components are the parts of the path under root; prefixes are its root-relative
	// ancestors and itself, such as "a", "a/b", "a/b/c". Both are empty outside root.
	components, prefixes []string
}

// candidates returns the path as written and as resolved through symlinks.
func candidates(root, path string) []candidate {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	abs = filepath.Clean(abs)
	var out []candidate
	for _, pair := range [][2]string{{root, abs}, {realRoot(root), resolved(root, path)}} {
		c := candidate{abs: pair[1]}
		if rel, err := filepath.Rel(pair[0], pair[1]); err == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "." {
			c.components = strings.Split(rel, string(filepath.Separator))
			for i := range c.components {
				c.prefixes = append(c.prefixes, filepath.ToSlash(filepath.Join(c.components[:i+1]...)))
			}
		}
		out = append(out, c)
	}
	return out
}

func realRoot(root string) string {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		return r
	}
	return root
}

// match applies patterns in order: a slash-less pattern tests names, one with a slash tests
// paths. The last pattern that matches decides. Case is ignored on every platform, since on a
// case-insensitive filesystem .ENV is .env (CVE-2014-9390); a false positive costs one prompt.
func match(patterns, names, paths []string) bool {
	hit := false
	for _, raw := range patterns {
		neg := strings.HasPrefix(raw, "!")
		p := strings.ToLower(strings.TrimPrefix(raw, "!"))
		pool := names
		if strings.Contains(p, "/") {
			pool = paths
		}
		for _, c := range pool {
			if ok, _ := filepath.Match(p, strings.ToLower(c)); ok {
				hit = !neg
				break
			}
		}
	}
	return hit
}

// resolved is path made absolute against root, with symlinks followed on its existing part.
func resolved(root, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	p, err := resolve(filepath.Clean(path))
	if err != nil {
		return path
	}
	return p
}

// Inside reports whether path, taken relative to root, resolves under root. Symlinks are
// followed on the longest part of the path that exists, so a link out of the tree counts as
// outside and a file not yet created counts by its parent.
func Inside(root, path string) bool {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	p, err := resolve(filepath.Clean(path))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolve follows symlinks on the longest existing prefix of an absolute path and keeps the
// rest as written.
func resolve(p string) (string, error) {
	var rest []string
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{real}, rest...)...), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", os.ErrNotExist
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}
