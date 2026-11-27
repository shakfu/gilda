package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/mock"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/provider"
	"github.com/shakfu/gilda/tool"
)

func TestParseBrief(t *testing.T) {
	src := "\n# Objective #\n\nshared context\n\n## One\nfirst\n### detail\nmore\n\n" +
		"## Two ##\n```md\n# not a heading\n## nor this\n```\n~~~\n## nor this\n~~~\nafter\n\n##   Three\n"
	b, err := ParseBrief(src)
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "Objective" || b.Body != "shared context" {
		t.Fatalf("objective %q, body %q", b.Title, b.Body)
	}
	want := []Section{
		{Title: "One", Body: "first\n### detail\nmore"},
		{Title: "Two", Body: "```md\n# not a heading\n## nor this\n```\n~~~\n## nor this\n~~~\nafter"},
		{Title: "Three"},
	}
	if !reflect.DeepEqual(b.Parts, want) {
		t.Fatalf("parts %+v\nwant %+v", b.Parts, want)
	}
}

func TestParseBriefReadsDepends(t *testing.T) {
	src := "# Goal\n\n## Survey\nlook around\n\n## Plan\nDepends: survey\nthen plan\n" +
		"```\ndepends: 4\n```\n\n## Build\ndepends: 2, Survey\ndepends: plan\n\n## Review\ndepends: 3\n"
	b, err := ParseBrief(src)
	if err != nil {
		t.Fatal(err)
	}
	got := make([][]int, len(b.Parts))
	for i, p := range b.Parts {
		got[i] = p.Depends
	}
	if want := [][]int{nil, {0}, {1, 0}, {2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("depends %v, want %v", got, want)
	}
	// The depends: line leaves the context; the one inside the fence is text.
	if b.Parts[1].Body != "then plan\n```\ndepends: 4\n```" {
		t.Fatalf("plan's body %q", b.Parts[1].Body)
	}
}

func TestParseBriefRefusesBadDepends(t *testing.T) {
	for src, want := range map[string]string{
		"# A\ndepends: 1\n## B\n":                                     "line 2: depends: under the # heading",
		"# A\n## B\ndepends: C\n":                                     `line 3: depends: no part titled "C"`,
		"# A\n## B\ndepends: 2\n":                                     "line 3: depends: no part 2; the brief has 1",
		"# A\n## B\ndepends: b\n":                                     `line 3: depends: part "B" depends on itself`,
		"# A\n## B\ndepends: ,\n":                                     "line 3: depends: names no part",
		"# A\n## X\n## X\n## Y\ndepends: x\n":                         `line 5: depends: "x" names parts 1 and 2; use a number`,
		"# A\n## P\ndepends: R\n## Q\ndepends: P\n## R\ndepends: Q\n": `depends: cycle "P" -> "R" -> "Q" -> "P"`,
	} {
		_, err := ParseBrief(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", src, err, want)
		}
	}
}

// Parts start only after what they depend on has finished, within the bound, and a wait cut by
// the context skips the part.
func TestScheduleOrdersParts(t *testing.T) {
	// 0 and 1 are independent; 2 needs both; 3 needs 2.
	deps := [][]int{nil, nil, {0, 1}, {2}}
	for _, n := range []int{0, 1, 2} {
		var mu sync.Mutex
		var order []int
		finished := map[int]bool{}
		running, peak := 0, 0
		schedule(context.Background(), deps, n, func(i int) {
			mu.Lock()
			for _, d := range deps[i] {
				if !finished[d] {
					t.Errorf("n=%d: %d started before %d finished", n, i, d)
				}
			}
			running++
			peak = max(peak, running)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			running--
			finished[i] = true
			order = append(order, i)
			mu.Unlock()
		}, func(i int, err error) { t.Errorf("n=%d: %d skipped: %v", n, i, err) })
		if len(order) != 4 || order[2] != 2 || order[3] != 3 {
			t.Errorf("n=%d: order %v", n, order)
		}
		if limit := map[int]int{0: 2, 1: 1, 2: 2}[n]; peak != limit {
			t.Errorf("n=%d: %d ran at once, want %d", n, peak, limit)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	skipped := map[int]error{}
	schedule(ctx, deps, 0, func(i int) {
		if i == 0 {
			cancel()
		}
	}, func(i int, err error) {
		mu.Lock()
		skipped[i] = err
		mu.Unlock()
	})
	if !errors.Is(skipped[2], context.Canceled) || !errors.Is(skipped[3], context.Canceled) {
		t.Fatalf("skipped %v; want 2 and 3 cancelled", skipped)
	}
}

func TestParseBriefRefusesWhatItWouldGuess(t *testing.T) {
	for src, want := range map[string]string{
		"intro\n# A\n## B\n":    "line 1: text before the # heading",
		"## B\n# A\n":           "line 1: ## heading before the # heading",
		"# A\n## B\n# C\n":      "line 3: a second # heading",
		"# A\ntext\n":           "no ## heading",
		"no headings":           "line 1: text before",
		"":                      "no # heading",
		"# A\n##\n":             "line 2: empty heading",
		"#A\n":                  "line 1: text before",
		"# A\n```\n## B\n```\n": "no ## heading",
	} {
		_, err := ParseBrief(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", src, err, want)
		}
	}
}

func TestPromptsCarryTheirContext(t *testing.T) {
	b := &Brief{Title: "Goal", Body: "shared", Parts: []Section{
		{Title: "A", Body: "about a"}, {Title: "B", Body: "about b"}, {Title: "C", Depends: []int{0, 1}},
	}}
	results, failed := []string{"a found x", "(no result: boom)", ""}, []bool{false, true, false}
	if p := b.partPrompt(0, results, failed); strings.Contains(p, "builds on") {
		t.Errorf("a part without dependencies got results:\n%s", p)
	}
	c := b.partPrompt(2, results, failed)
	for _, s := range []string{"### Result of part: A\n\na found x", "### Result of part: B\n\n[this part failed", "(no result: boom)"} {
		if !strings.Contains(c, s) {
			t.Errorf("dependent prompt lacks %q:\n%s", s, c)
		}
	}
	p := b.partPrompt(1, results, failed)
	for _, s := range []string{"# Objective: Goal", "shared", "- A\n", "## Your part: B", "about b"} {
		if !strings.Contains(p, s) {
			t.Errorf("part prompt lacks %q:\n%s", s, p)
		}
	}
	if strings.Contains(p, "about a") || strings.Contains(p, "- B\n") {
		t.Errorf("part prompt has another part's context or itself as another part:\n%s", p)
	}
	s := b.synthesisPrompt([]string{"a's answer", "(no result: boom)", "c's answer"}, []bool{false, true, false})
	for _, w := range []string{"# Objective: Goal", "## Part 1: A\n\na's answer", "## Part 2: B\n\n[this part failed", "(no result: boom)"} {
		if !strings.Contains(s, w) {
			t.Errorf("synthesis prompt lacks %q:\n%s", w, s)
		}
	}
}

func TestTheSampleBriefParses(t *testing.T) {
	src, err := os.ReadFile("brief.md")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBrief(string(src))
	if err != nil || len(b.Parts) != 3 {
		t.Fatalf("brief.md: %v, %d parts", err, len(b.Parts))
	}
	if !reflect.DeepEqual(b.Parts[1].Depends, []int{0}) {
		t.Fatalf("brief.md: the refusal part depends on %v, want [0]", b.Parts[1].Depends)
	}
}

// An app that brings its own Pricer links neither app, state, price nor their dependencies.
func TestLinksNoSessionStateOrPriceList(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range strings.Fields(string(out)) {
		for _, bad := range []string{"/gilda/app", "/gilda/state", "/gilda/price", "/gilda/tui", "/gilda/acp", "BurntSushi/toml", "charm.land/"} {
			if strings.Contains(p, bad) {
				t.Errorf("links %s", p)
			}
		}
	}
}

func resultOf(in, out int64, cost float64) agent.Result {
	return agent.Result{Usage: llm.Usage{Input: in, Output: out, Cost: llm.Float(cost)}}
}

func TestCheckFindsEachBrokenInvariant(t *testing.T) {
	t0 := time.Now()
	ok := func() *Report {
		return &Report{
			// b depends on a, and started when a ended.
			Brief: &Brief{Parts: []Section{{Title: "a"}, {Title: "b", Depends: []int{0}}}},
			Parts: []Outcome{
				{Title: "a", Result: resultOf(100, 10, 0.5), Start: t0, End: t0.Add(time.Second)},
				{Title: "b", Result: resultOf(50, 5, 0.25), Start: t0.Add(time.Second), End: t0.Add(2 * time.Second)},
			},
			Final:  &Outcome{Title: "goal", Result: resultOf(20, 2, 0.1)},
			Meter:  Totals{Responses: 3, Unpriced: 3, Usage: llm.Usage{Input: 170, Output: 17}, Peak: 2},
			Priced: 3, Charged: 0.85,
		}
	}
	if bad := ok().Check(); len(bad) > 0 {
		t.Fatalf("a consistent report fails: %v", bad)
	}
	for name, c := range map[string]struct {
		spoil func(*Report)
		want  string
	}{
		"tokens": {func(r *Report) { r.Meter.Usage.Input++ }, "tokens:"},
		"final":  {func(r *Report) { r.Final = nil }, "tokens:"},
		"cost":   {func(r *Report) { r.Charged = 1 }, "cost:"},
		"priced": {func(r *Report) { r.Priced = 1 }, "priced:"},
		"serial": {func(r *Report) { r.Meter.Peak = 1 }, "parallel:"},
		"done":   {func(r *Report) { r.Parts[0].Done = 1 }, "TaskDone events"},
		"nested": {func(r *Report) { r.Parts[1].Nested = 1 }, "nested TaskEvents"},
		"labels": {func(r *Report) { r.Final.Labels = 2 }, "named a running task's call"},
		"order":  {func(r *Report) { r.Parts[1].Start = t0 }, `order: "b" started before "a"`},
		"unrun":  {func(r *Report) { r.Parts[0].End = time.Time{} }, `order: "b" started before "a"`},
	} {
		r := ok()
		c.spoil(r)
		bad := strings.Join(r.Check(), "\n")
		if !strings.Contains(bad, c.want) {
			t.Errorf("%s: Check reported %q", name, bad)
		}
	}
}

func TestReportMarksFailures(t *testing.T) {
	r := &Report{
		Brief: &Brief{Title: "Goal"},
		Parts: []Outcome{{Title: "a", Result: agent.Result{Text: "half"}, Err: errors.New("stopped")}, {Title: "b", Err: errors.New("boom")}},
	}
	var w bytes.Buffer
	r.Write(&w)
	for _, s := range []string{"# Goal\n\n(not addressed", "## a\n\nhalf\n\n[stopped early: stopped]", "## b\n\n(failed: boom)"} {
		if !strings.Contains(w.String(), s) {
			t.Errorf("report lacks %q:\n%s", s, w.String())
		}
	}
}

func TestRunningLabelsReadsOnlyTaskCollect(t *testing.T) {
	h := []llm.Message{
		{Role: llm.Assistant, Calls: []llm.ToolCall{{ID: "1", Name: "task_collect"}, {ID: "2", Name: "bash"}}},
		{Role: llm.Tool, Results: []llm.ToolResult{
			{CallID: "1", Content: `t1 "w" running, 2 turns, last: $ ls`},
			{CallID: "2", Content: `t1 "w" running, 2 turns, last: $ ls`},
		}},
	}
	if n := runningLabels(h); n != 1 {
		t.Fatalf("%d labels, want 1", n)
	}
}

func TestReadOnlyOffersNoBash(t *testing.T) {
	names := func(mode permission.Mode) []string {
		s := &swarm{o: Options{Dir: t.TempDir(), Mode: mode}, jobs: &tool.Jobs{}}
		var out []string
		for _, tl := range s.tools() {
			out = append(out, tl.Spec().Name)
		}
		return out
	}
	if got := names(permission.ReadOnly); !reflect.DeepEqual(got, []string{"read"}) {
		t.Errorf("read-only: %v, want [read]", got)
	}
	if got := names(permission.Auto); !reflect.DeepEqual(got, []string{"read", "bash"}) {
		t.Errorf("auto: %v, want [read bash]", got)
	}
}

// The tools a part agent is offered, by mode, with depth 1.
func TestReadOnlyOffersNoTasks(t *testing.T) {
	offered := func(mode permission.Mode) []string {
		p := mock.New(mock.Step{Text: "ok"})
		s := &swarm{cfg: agent.Config{Provider: p, Model: "m"}, o: Options{Dir: t.TempDir(), Mode: mode},
			jobs: &tool.Jobs{}, logf: func(string, ...any) {}}
		if out := s.run(context.Background(), "p1", "t", "", "go", 1); out.Err != nil {
			t.Fatal(out.Err)
		}
		var names []string
		for _, spec := range p.Requests[0].Tools {
			names = append(names, spec.Name)
		}
		return names
	}
	if got := offered(permission.ReadOnly); !reflect.DeepEqual(got, []string{"read"}) {
		t.Errorf("read-only: %v, want [read]", got)
	}
	if got := offered(permission.Auto); !reflect.DeepEqual(got, []string{"read", "bash", "task", "task_collect"}) {
		t.Errorf("auto: %v, want [read bash task task_collect]", got)
	}
}

// A call the mode refuses outright is logged, though it never reaches an ask function.
func TestRefusedCallsAreLogged(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	approve := refusals(permission.Approver(permission.ReadOnly, dir, nil), logf)
	env := tool.Env{Root: dir, Jobs: &tool.Jobs{}}
	ok, err := approve(context.Background(), tool.Bash{Env: env}, llm.ToolCall{Name: "bash", Arguments: `{"command":"ls"}`}, "$ ls")
	if ok || err == nil {
		t.Fatalf("bash in read-only: ok %v, err %v; want a refusal", ok, err)
	}
	refusal := err
	ok, err = approve(context.Background(), tool.Read{Env: env}, llm.ToolCall{Name: "read", Arguments: `{"path":"."}`}, "read .")
	if !ok || err != nil {
		t.Fatalf("read in read-only: ok %v, err %v; want run", ok, err)
	}
	if want := []string{"  $ ls: " + refusal.Error()}; !reflect.DeepEqual(lines, want) {
		t.Errorf("logged %q, want %q", lines, want)
	}
}

// live skips unless SWARM_LIVE is set and a provider key is: the run spends money.
func live(t *testing.T) (*Brief, Options) {
	if os.Getenv("SWARM_LIVE") == "" {
		t.Skip("set SWARM_LIVE=1 and a provider key to run against a real provider")
	}
	if _, err := provider.Choose(os.Getenv("SWARM_PROVIDER"), nil); err != nil {
		t.Skip(err)
	}
	src, err := os.ReadFile("brief.md")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBrief(string(src))
	if err != nil {
		t.Fatal(err)
	}
	return b, Options{
		Dir:      "../..",
		Provider: os.Getenv("SWARM_PROVIDER"),
		Model:    os.Getenv("SWARM_MODEL"),
		Mode:     permission.ReadOnly,
		Depth:    1,
		Running:  4,
		MaxTurns: 32,
		PriceIn:  1, PriceOut: 5,
		Log: testLog{t},
	}
}

type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func TestLiveBriefIsAnsweredFromItsParts(t *testing.T) {
	b, o := live(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	rep, err := Run(ctx, b, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range rep.Check() {
		t.Error(bad)
	}
	for _, p := range rep.Parts {
		if strings.TrimSpace(p.Result.Text) == "" {
			t.Errorf("part %q: no answer", p.Title)
		}
	}
	if rep.Final == nil || strings.TrimSpace(rep.Final.Result.Text) == "" {
		t.Fatal("no synthesis")
	}
	var w bytes.Buffer
	rep.Write(&w)
	t.Logf("%s\n\n%s", rep.Summary(), w.String())
}

// copyRepo copies the repository at src into a temporary directory, without its git metadata
// or build output, so an agent in auto mode writes only to the copy.
func copyRepo(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	skip := map[string]bool{".git": true, ".quarto": true, "bin": true, "results": true}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		switch {
		case d.IsDir() && skip[d.Name()]:
			return filepath.SkipDir
		case d.IsDir():
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// In auto mode the parts get task tools; asked to, they start background tasks, and every
// task invariant holds over real ones. It runs on a copy, since auto lets bash write.
func TestLiveTasksInAutoMode(t *testing.T) {
	b, o := live(t)
	o.Dir, o.Mode = copyRepo(t, o.Dir), permission.Auto
	for i := range b.Parts {
		b.Parts[i].Body += "\n\nStart at least two background tasks (task with background: true) " +
			"for independent searches, and collect every answer with task_collect before you reply."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	rep, err := Run(ctx, b, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range rep.Check() {
		t.Error(bad)
	}
	tasks := 0
	for _, p := range rep.Parts {
		tasks += len(p.Tasks)
	}
	if tasks == 0 {
		t.Error("no part started a background task, though each was asked to")
	}
	t.Log(rep.Summary())
}

// A deadline mid-run stops every part, with tasks in flight, and skips the synthesis.
func TestLiveDeadlineStopsEveryPart(t *testing.T) {
	b, o := live(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	rep, err := Run(ctx, b, o)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v; want the deadline", err)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("Run returned %v after an 8s deadline", d)
	}
	if rep.Final != nil {
		t.Error("the synthesis ran after the deadline")
	}
	for _, p := range rep.Parts {
		for _, h := range p.Tasks {
			if strings.Contains(h.Status(), `" running, `) {
				t.Errorf("%q: %s", p.Title, h.Status())
			}
		}
	}
}
