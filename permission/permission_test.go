package permission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/tool"
)

func call(name string, args map[string]string) llm.ToolCall {
	data, _ := json.Marshal(args)
	return llm.ToolCall{Name: name, Arguments: string(data)}
}

// custom is a tool with no declarations: it may change anything.
type custom struct{ name string }

func (c custom) Spec() llm.ToolSpec                                      { return llm.ToolSpec{Name: c.name} }
func (custom) Label(json.RawMessage) string                              { return "" }
func (custom) Run(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil }

// grep declares itself read-only and names the file it reads.
type grep struct{ custom }

func (grep) ReadOnly() bool { return true }
func (grep) Paths(a json.RawMessage) ([]string, error) {
	var v struct{ Path string }
	err := json.Unmarshal(a, &v)
	return []string{v.Path}, err
}

// patch modifies files and names them.
type patch struct{ custom }

func (patch) Paths(a json.RawMessage) ([]string, error) {
	var v struct{ Path string }
	err := json.Unmarshal(a, &v)
	return []string{v.Path}, err
}

func TestDecisionsFollowDeclarations(t *testing.T) {
	root := t.TempDir()
	env := tool.Env{Root: root}
	outside := filepath.Join(t.TempDir(), "x")
	type c struct {
		t    tool.Tool
		call llm.ToolCall
	}
	cases := map[string]c{
		"read out":      {tool.Read{Env: env}, call("read", map[string]string{"path": outside})},
		"read .env":     {tool.Read{Env: env}, call("read", map[string]string{"path": ".env.local"})},
		"read .env.ex":  {tool.Read{Env: env}, call("read", map[string]string{"path": ".env.example"})},
		"write in":      {tool.Write{Env: env}, call("write", map[string]string{"path": "a/new.txt"})},
		"write out":     {tool.Write{Env: env}, call("write", map[string]string{"path": outside})},
		"edit up":       {tool.Edit{Env: env}, call("edit", map[string]string{"path": "../escape"})},
		"write .git":    {tool.Write{Env: env}, call("write", map[string]string{"path": ".git/config"})},
		"write .env":    {tool.Write{Env: env}, call("write", map[string]string{"path": "cfg/.env"})},
		"write .envrc":  {tool.Write{Env: env}, call("write", map[string]string{"path": ".envrc"})},
		"write .github": {tool.Write{Env: env}, call("write", map[string]string{"path": ".github/ci.yml"})},
		"write junk":    {tool.Write{Env: env}, llm.ToolCall{Name: "write", Arguments: "{"}},
		"bash":          {tool.Bash{Env: env}, call("bash", map[string]string{"command": "ls"})},
		"grep":          {grep{custom{"grep"}}, call("grep", map[string]string{"path": "main.go"})},
		"grep .env":     {grep{custom{"grep"}}, call("grep", map[string]string{"path": ".env"})},
		"patch in":      {patch{custom{"patch"}}, call("patch", map[string]string{"path": "main.go"})},
		"patch out":     {patch{custom{"patch"}}, call("patch", map[string]string{"path": outside})},
		"undeclared":    {custom{"deploy"}, call("deploy", nil)},
		"named bash":    {custom{"bash"}, call("bash", nil)},
		"named read":    {custom{"read"}, call("read", map[string]string{"path": "x"})},
	}
	// r runs, a asks, x refuses.
	want := map[Mode]string{
		//         read out, read .env, read .env.ex, write in, write out, edit up, write .git, write .env, write .envrc,
		//         write .github, write junk, bash, grep, grep .env, patch in, patch out, undeclared, named bash, named read
		Auto:     "r a r r a a a a r r r r r a r a a a a",
		Ask:      "r a r a a a a a a a a a r a a a a a a",
		All:      "r r r r r r r r r r r r r r r r r r r",
		ReadOnly: "r x r x x x x x x x x x r x x x x x x",
	}
	order := strings.Fields("read-out read-.env read-.env.ex write-in write-out edit-up write-.git write-.env " +
		"write-.envrc write-.github write-junk bash grep grep-.env patch-in patch-out undeclared named-bash named-read")
	for mode, row := range want {
		verdicts := strings.Fields(row)
		for i, key := range order {
			k := strings.ReplaceAll(key, "-", " ")
			cs, ok := cases[k]
			if !ok {
				t.Fatalf("no case %q", k)
			}
			v, _ := Layers(nil).decide(mode, root, cs.t, cs.call)
			got := map[verdict]string{run: "r", askUser: "a", refuse: "x"}[v]
			if got != verdicts[i] {
				t.Errorf("%s / %s: got %s, want %s", mode, k, got, verdicts[i])
			}
		}
	}
}

func TestRefusalsSayWhy(t *testing.T) {
	root := t.TempDir()
	env := tool.Env{Root: root}
	approve := Approver(Auto, root, nil)
	_, err := approve(context.Background(), tool.Write{Env: env}, call("write", map[string]string{"path": ".git/HEAD"}), "")
	if err == nil || !strings.Contains(err.Error(), "under .git") {
		t.Fatalf("got %v", err)
	}
	_, err = approve(context.Background(), tool.Read{Env: env}, call("read", map[string]string{"path": ".env"}), "")
	if err == nil || !strings.Contains(err.Error(), "may hold secrets") {
		t.Fatalf("got %v", err)
	}
	asked, kind := "", General
	yes := func(_ context.Context, _ tool.Tool, _ llm.ToolCall, label string, why Reason) (bool, error) {
		asked, kind = label, why.Kind
		return true, nil
	}
	ok, err := Approver(Auto, root, yes)(context.Background(), tool.Read{Env: env}, call("read", map[string]string{"path": ".env"}), "read .env")
	if !ok || err != nil || asked != "read .env" || kind != Secret {
		t.Fatalf("ask: %v %v %q %d", ok, err, asked, kind)
	}
}

// A secret or protected path is reported as such whatever else about the call asks, so an
// approval remembered for the other reason never covers it.
func TestSensitiveReasonsWin(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	env := tool.Env{Root: root}
	cases := []struct {
		mode Mode
		t    tool.Tool
		path string
		want Kind
	}{
		{Auto, tool.Write{Env: env}, filepath.Join(other, "f.txt"), Outside},
		{Auto, tool.Write{Env: env}, filepath.Join(other, ".env"), Secret},
		{Auto, tool.Write{Env: env}, ".git/config", Protected},
		{Auto, tool.Read{Env: env}, ".env", Secret},
		{Ask, tool.Write{Env: env}, "main.go", General},
		{Ask, tool.Write{Env: env}, ".env", Secret},
		{Ask, tool.Write{Env: env}, ".git/config", Protected},
	}
	for _, c := range cases {
		name := c.t.Spec().Name
		v, why := Layers(nil).decide(c.mode, root, c.t, call(name, map[string]string{"path": c.path}))
		if v != askUser || why.Kind != c.want {
			t.Errorf("%s %s in %s: got %d kind %d (%s), want ask kind %d", name, c.path, c.mode, v, why.Kind, why, c.want)
		}
		if why.Lasting() != (c.want != Secret && c.want != Protected) {
			t.Errorf("%s %s: Lasting() = %v", name, c.path, why.Lasting())
		}
	}
}

// A symlink inside the root that points out of it is outside, and one that points at a secret
// is a secret.
func TestSymlinksAreFollowed(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	if err := os.Symlink(other, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ".env"), []byte("K=v"), 0o600)
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "innocent")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"f.txt": true, "new/dir/f.txt": true, "link/f.txt": false, "../x": false,
		filepath.Join(root, "abs"): true, other: false,
	}
	for p, want := range cases {
		if got := Inside(root, p); got != want {
			t.Errorf("Inside(%q) = %v, want %v", p, got, want)
		}
	}
	if v, _ := Layers(nil).decide(Auto, root, tool.Read{Env: tool.Env{Root: root}}, call("read", map[string]string{"path": "innocent"})); v != askUser {
		t.Error("a symlink to .env was read without asking")
	}
	if Layers(nil).Protects(root, "innocent") != "innocent" {
		t.Error("a write through a symlink to .env is not protected")
	}
}

func TestBuiltinSecrets(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, ".env.local": true, "dir/.env.production": true,
		".envrc": false, ".env.example": false, ".env.sample": false, ".env.template": false,
		"env": false, "config.env": false,
		"id_rsa": true, "home/.ssh/id_ed25519": true, "id_rsa.pub": false, "id_ed25519.pub": false,
		"config/master.key": true, "cert.p12": true, "store.jks": true, "vault.kdbx": true,
		"keyboard.go": false, "keys.go": false, "cert.pem": false,
		".git-credentials": true, ".netrc": true, ".pgpass": true, ".pypirc": true,
		"credentials": true, "credentials.json": true, "credentials.go": false,
		"terraform.tfstate": true, "terraform.tfstate.backup": true, "main.tf": false,
	} {
		if Layers(nil).IsSecret("/r", name) != want {
			t.Errorf("IsSecret(%q) = %v", name, !want)
		}
	}
}

func TestParse(t *testing.T) {
	if m, err := Parse(""); m != Auto || err != nil {
		t.Fatal("empty is not auto")
	}
	if _, err := Parse("yolo"); err == nil {
		t.Fatal("accepted an unknown mode")
	}
}

func TestRulesAddToTheBuiltInOnes(t *testing.T) {
	root := t.TempDir()
	env := tool.Env{Root: root}
	rules := Rules{
		Secrets:   []string{"*.pem", "!public.pem", "secrets/*", "!.env"},
		Protected: []string{"!.git", "config/prod", "go.sum"},
	}
	if err := rules.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		t    tool.Tool
		path string
		want verdict
	}{
		{tool.Read{Env: env}, "certs/server.pem", askUser},
		{tool.Read{Env: env}, "certs/public.pem", run},
		{tool.Read{Env: env}, "secrets/token", askUser},
		{tool.Read{Env: env}, "secrets", run},
		{tool.Read{Env: env}, "config/prod/db.yaml", run},
		{tool.Write{Env: env}, "config/prod/db.yaml", askUser},
		{tool.Write{Env: env}, "config/production.yaml", run},
		{tool.Write{Env: env}, "sub/go.sum", askUser},
		// A user's "!" cannot lift a built-in rule.
		{tool.Write{Env: env}, ".git/config", askUser},
		{tool.Read{Env: env}, ".env", askUser},
	}
	for _, c := range cases {
		name := c.t.Spec().Name
		v, why := Layers{rules}.decide(Auto, root, c.t, call(name, map[string]string{"path": c.path}))
		if v != c.want {
			t.Errorf("%s %s: got %d (%s), want %d", name, c.path, v, why, c.want)
		}
	}
	if part := (Layers{rules}).Protects(root, "config/prod/db.yaml"); part != "config/prod" {
		t.Errorf("protected part %q", part)
	}
	for _, bad := range []string{"[", "!", ""} {
		if err := (Rules{Secrets: []string{bad}}).Validate(); err == nil {
			t.Errorf("accepted pattern %q", bad)
		}
	}
	b := Builtin()
	b.Protected[0] = "changed"
	if Builtin().Protected[0] != ".git" {
		t.Fatal("Builtin exposed the rules it applies")
	}
}

func TestBuiltinProtectsVersionControl(t *testing.T) {
	root := t.TempDir()
	for path, want := range map[string]string{
		".git/config": ".git", ".hg/store": ".hg", "sub/.svn/wc.db": ".svn", ".jj/repo": ".jj",
		".github/ci.yml": "", ".gitignore": "", "src/main.go": "",
	} {
		if got := Layers(nil).Protects(root, path); got != want {
			t.Errorf("Protects(%q) = %q, want %q", path, got, want)
		}
	}
}

// A "!" exempts only within its own layer: an embedding app cannot lift what the user's
// settings protect, and the reverse.
func TestLayersDoNotExemptEachOther(t *testing.T) {
	root := t.TempDir()
	user := Rules{Secrets: []string{"*.pem"}, Protected: []string{"migrations"}}
	app := Rules{Secrets: []string{"!*.pem"}, Protected: []string{"!migrations", "vendor"}}
	l := Layers{user, app}
	if !l.IsSecret(root, "server.pem") {
		t.Error("the app's layer lifted the user's secret")
	}
	if l.Protects(root, "migrations/001.sql") != "migrations" {
		t.Error("the app's layer lifted the user's protection")
	}
	if l.Protects(root, "vendor/x.go") != "vendor" {
		t.Error("the app's own protection is missing")
	}
	own := Layers{{Secrets: []string{"*.pem", "!public.pem"}}}
	if own.IsSecret(root, "public.pem") || !own.IsSecret(root, "private.pem") {
		t.Error("an exemption within a layer does not work")
	}
}

// Tools built with tool.New are judged by what their Def declares.
func TestDefToolsFollowTheirDeclarations(t *testing.T) {
	root := t.TempDir()
	noop := func(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil }
	path := func(a json.RawMessage) ([]string, error) {
		var v struct{ Path string }
		err := json.Unmarshal(a, &v)
		return []string{v.Path}, err
	}
	grep, _ := tool.New(tool.Def{Name: "grep", ReadOnly: true, Paths: path, Run: noop})
	fmt_, _ := tool.New(tool.Def{Name: "fmt", Paths: path, Run: noop})
	deploy, _ := tool.New(tool.Def{Name: "deploy", Run: noop})
	cases := []struct {
		t    tool.Tool
		path string
		mode Mode
		want verdict
	}{
		{grep, "main.go", ReadOnly, run},
		{grep, ".env", Auto, askUser},
		{fmt_, "main.go", Auto, run},
		{fmt_, "/etc/hosts", Auto, askUser},
		{fmt_, ".git/config", Auto, askUser},
		{fmt_, "main.go", ReadOnly, refuse},
		{deploy, "", Auto, askUser},
	}
	for _, c := range cases {
		v, why := Layers(nil).decide(c.mode, root, c.t, call(c.t.Spec().Name, map[string]string{"path": c.path}))
		if v != c.want {
			t.Errorf("%s %s in %s: got %d (%s), want %d", c.t.Spec().Name, c.path, c.mode, v, why, c.want)
		}
	}
}

// On a case-insensitive filesystem .ENV is .env, so patterns ignore case everywhere.
func TestPatternsIgnoreCase(t *testing.T) {
	root := t.TempDir()
	env := tool.Env{Root: root}
	for _, c := range []struct {
		t    tool.Tool
		path string
		want Kind
	}{
		{tool.Read{Env: env}, ".ENV", Secret},
		{tool.Read{Env: env}, "ID_RSA", Secret},
		{tool.Write{Env: env}, ".GIT/hooks/pre-commit", Protected},
		{tool.Write{Env: env}, "sub/.Hg/store", Protected},
	} {
		name := c.t.Spec().Name
		v, why := Layers(nil).decide(Auto, root, c.t, call(name, map[string]string{"path": c.path}))
		if v != askUser || why.Kind != c.want {
			t.Errorf("%s %s: got %d (%s)", name, c.path, v, why)
		}
	}
	user := Layers{{Secrets: []string{"*.PEM", "!Public.pem"}, Protected: []string{"Config/Prod"}}}
	if !user.IsSecret(root, "server.pem") || user.IsSecret(root, "PUBLIC.PEM") {
		t.Error("user patterns are case-sensitive")
	}
	if user.Protects(root, "config/prod/db.yaml") == "" {
		t.Error("a slashed pattern is case-sensitive")
	}
	if Layers(nil).IsSecret(root, ".envrc") {
		t.Error(".envrc became a secret")
	}
}

// Where the filesystem folds case, a path spelled in another case reaches the same file and
// must be checked as that file.
func TestCaseInsensitiveFilesystem(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "probe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "PROBE")); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	os.WriteFile(filepath.Join(root, ".env"), []byte("K=v"), 0o600)
	os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755)
	env := tool.Env{Root: root}
	if v, _ := Layers(nil).decide(Auto, root, tool.Read{Env: env}, call("read", map[string]string{"path": ".ENV"})); v != askUser {
		t.Error("read .ENV ran without asking")
	}
	if v, _ := Layers(nil).decide(Auto, root, tool.Write{Env: env}, call("write", map[string]string{"path": ".GIT/hooks/pre-commit"})); v != askUser {
		t.Error("write .GIT/hooks/pre-commit ran without asking")
	}
}
