// Command swarm works on a markdown brief with parallel agents. The brief's level-1 heading is
// the objective, and each level-2 heading a part of it; the text under a heading is its
// context. One agent works on each part, all at once. When every part has returned, one more
// agent addresses the objective from their results.
//
// A "depends: <ref>, ..." line under a part makes it wait for those parts and read their
// results. A ref is a part's title, ignoring case, or its number from 1.
//
//	swarm -C ~/src/project brief.md > report.md
//
// It embeds gilda's library without app, state or price, and after the run checks that usage,
// cost and tasks add up across every agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/provider"
)

func main() {
	var o Options
	var mode string
	timeout := flag.Duration("timeout", 30*time.Minute, "cancel the run after this long")
	outPath := flag.String("o", "", "write the report to this file instead of stdout")
	flag.StringVar(&o.Dir, "C", ".", "directory the agents work in")
	flag.StringVar(&o.Provider, "P", "", "provider: "+strings.Join(provider.IDs(), ", ")+"; default the first with a key")
	flag.StringVar(&o.Model, "m", "", "model; default the provider's")
	flag.StringVar(&mode, "permissions", string(permission.ReadOnly), "permission mode for every agent")
	flag.IntVar(&o.Parallel, "parallel", 0, "part agents running at once; 0 runs every part at once")
	flag.IntVar(&o.Depth, "depth", 1, "how deep a part agent's own background tasks nest; 0 gives it none, as read-only mode does")
	flag.IntVar(&o.Running, "running", 4, "background tasks running at once per part")
	flag.Int64Var(&o.Budget, "budget", 0, "tokens the background tasks of one part may use; 0 is unbounded")
	flag.IntVar(&o.MaxTurns, "max-turns", 32, "round-trips per prompt")
	flag.Float64Var(&o.PriceIn, "price-in", 0, "USD per million input tokens, for providers that report no cost")
	flag.Float64Var(&o.PriceOut, "price-out", 0, "USD per million output tokens")
	quiet := flag.Bool("quiet", false, "no progress lines")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: swarm [flags] BRIEF.md\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	o.Mode = permission.Mode(mode)
	if !*quiet {
		o.Log = os.Stderr
	}
	src, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fail(err)
	}
	b, err := ParseBrief(string(src))
	if err != nil {
		fail(fmt.Errorf("%s: %w", flag.Arg(0), err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	rep, err := Run(ctx, b, o)
	if rep == nil {
		fail(err)
	}
	out := io.Writer(os.Stdout)
	if *outPath != "" {
		f, ferr := os.Create(*outPath)
		if ferr != nil {
			fail(ferr)
		}
		defer f.Close()
		out = f
	}
	rep.Write(out)
	fmt.Fprintln(os.Stderr, rep.Summary())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	bad := rep.Check()
	for _, b := range bad {
		fmt.Fprintln(os.Stderr, "FAIL", b)
	}
	if err != nil || len(bad) > 0 {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "swarm:", err)
	os.Exit(1)
}

// Write prints the report as markdown: the objective's answer, then each part's.
func (r *Report) Write(w io.Writer) {
	fmt.Fprintf(w, "# %s\n\n", r.Brief.Title)
	switch {
	case r.Final == nil:
		fmt.Fprint(w, "(not addressed: the synthesis did not run)\n\n")
	default:
		fmt.Fprintf(w, "%s\n\n", answer(*r.Final))
	}
	fmt.Fprint(w, "---\n\n")
	for _, p := range r.Parts {
		fmt.Fprintf(w, "## %s\n\n%s\n\n", p.Title, answer(p))
	}
}

func answer(o Outcome) string {
	text := strings.TrimSpace(o.Result.Text)
	switch {
	case o.Err != nil && text != "":
		return text + "\n\n[stopped early: " + o.Err.Error() + "]"
	case o.Err != nil:
		return "(failed: " + o.Err.Error() + ")"
	case text == "":
		return "(no answer)"
	}
	return text
}

// Summary is one line of totals.
func (r *Report) Summary() string {
	mt := r.Meter
	cost := "cost unknown"
	if mt.Usage.Cost != nil || r.Charged > 0 {
		c := r.Charged
		if mt.Usage.Cost != nil {
			c += *mt.Usage.Cost
		}
		cost = fmt.Sprintf("$%.4f", c)
	}
	tasks := 0
	for _, p := range r.Parts {
		tasks += len(p.Tasks)
	}
	return fmt.Sprintf("%s/%s: %d parts, %d background tasks, %d responses (%d failed), %d in flight at most, %d+%d tokens, %s, %s",
		r.Provider, r.Model, len(r.Parts), tasks, mt.Responses, mt.Failed, mt.Peak, mt.Usage.Input, mt.Usage.Output, cost, r.Elapsed.Round(time.Millisecond))
}
