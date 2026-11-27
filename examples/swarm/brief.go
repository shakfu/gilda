package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Brief is a markdown document: one objective as the level-1 heading, and the parts it splits
// into as level-2 headings. Text under a heading, deeper headings included, is its context.
type Brief struct {
	Title, Body string
	Parts       []Section
}

type Section struct {
	Title, Body string
	// Depends holds the indices of the parts that must finish before this one starts, from its
	// depends: lines.
	Depends []int
}

// dependsLine matches a part's "depends: A, 2" line. Each reference is a part's title, ignoring
// case, or its number from 1.
var dependsLine = regexp.MustCompile(`(?i)^ {0,3}depends:[ \t]*(.*?)[ \t]*$`)

// refs are a depends: line's references, kept until every part is known.
type refs struct {
	part, line int
	names      []string
}

// heading matches an ATX heading: up to three spaces, 1-6 #, then a space or the line's end.
// A closing run of # is dropped.
var heading = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)

// fence matches the opening or closing line of a fenced code block.
var fence = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// ParseBrief splits src into its objective and parts. Headings and depends: lines inside fenced
// code blocks are text. Text before the level-1 heading, a second one, no level-2 heading, or a
// depends: line that names no single part or makes a cycle is an error, since each would mean
// guessing what the author meant.
func ParseBrief(src string) (*Brief, error) {
	var b *Brief
	var body *strings.Builder
	flush := func() {}
	var open string // the fence that opened the current code block
	var pending []refs
	for i, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		n := i + 1
		if f := fence.FindStringSubmatch(line); f != nil {
			switch {
			case open == "":
				open = f[1]
			case f[1][0] == open[0] && len(f[1]) >= len(open) && strings.TrimSpace(line) == f[1]:
				open = ""
			}
		}
		h := heading.FindStringSubmatch(line)
		if open != "" || h == nil || len(h[1]) > 2 || fence.MatchString(line) {
			if body == nil {
				if strings.TrimSpace(line) != "" {
					return nil, fmt.Errorf("line %d: text before the # heading", n)
				}
				continue
			}
			if d := dependsLine.FindStringSubmatch(line); d != nil && open == "" && !fence.MatchString(line) {
				if len(b.Parts) == 0 {
					return nil, fmt.Errorf("line %d: depends: under the # heading; only a part has dependencies", n)
				}
				var names []string
				for _, r := range strings.Split(d[1], ",") {
					if r = strings.TrimSpace(r); r != "" {
						names = append(names, r)
					}
				}
				if len(names) == 0 {
					return nil, fmt.Errorf("line %d: depends: names no part", n)
				}
				pending = append(pending, refs{part: len(b.Parts) - 1, line: n, names: names})
				continue
			}
			body.WriteString(line + "\n")
			continue
		}
		title := strings.TrimSpace(h[2])
		if title == "" {
			return nil, fmt.Errorf("line %d: empty heading", n)
		}
		flush()
		body = &strings.Builder{}
		switch {
		case len(h[1]) == 1 && b != nil:
			return nil, fmt.Errorf("line %d: a second # heading; a brief has one objective", n)
		case len(h[1]) == 1:
			b = &Brief{Title: title}
			flush = func() { b.Body = strings.TrimSpace(body.String()) }
		case b == nil:
			return nil, fmt.Errorf("line %d: ## heading before the # heading", n)
		default:
			b.Parts = append(b.Parts, Section{Title: title})
			k := len(b.Parts) - 1
			flush = func() { b.Parts[k].Body = strings.TrimSpace(body.String()) }
		}
	}
	flush()
	switch {
	case b == nil:
		return nil, errors.New("no # heading: a brief needs one objective")
	case len(b.Parts) == 0:
		return nil, errors.New("no ## heading: a brief needs at least one part")
	}
	for _, p := range pending {
		for _, name := range p.names {
			d, err := b.resolve(name)
			if err != nil {
				return nil, fmt.Errorf("line %d: depends: %w", p.line, err)
			}
			if d == p.part {
				return nil, fmt.Errorf("line %d: depends: part %q depends on itself", p.line, b.Parts[d].Title)
			}
			s := &b.Parts[p.part]
			if !slices.Contains(s.Depends, d) {
				s.Depends = append(s.Depends, d)
			}
		}
	}
	if c := b.cycle(); c != nil {
		titles := make([]string, len(c))
		for i, k := range c {
			titles[i] = fmt.Sprintf("%q", b.Parts[k].Title)
		}
		return nil, fmt.Errorf("depends: cycle %s", strings.Join(titles, " -> "))
	}
	return b, nil
}

// resolve finds the part a reference names: a number from 1, or a title, ignoring case.
func (b *Brief) resolve(name string) (int, error) {
	if n, err := strconv.Atoi(name); err == nil {
		if n < 1 || n > len(b.Parts) {
			return 0, fmt.Errorf("no part %d; the brief has %d", n, len(b.Parts))
		}
		return n - 1, nil
	}
	found := -1
	for i, s := range b.Parts {
		if strings.EqualFold(s.Title, name) {
			if found >= 0 {
				return 0, fmt.Errorf("%q names parts %d and %d; use a number", name, found+1, i+1)
			}
			found = i
		}
	}
	if found < 0 {
		return 0, fmt.Errorf("no part titled %q", name)
	}
	return found, nil
}

// cycle returns a dependency cycle as part indices, first repeated last, or nil.
func (b *Brief) cycle() []int {
	const (
		unseen = iota
		visiting
		visited
	)
	state := make([]int, len(b.Parts))
	var path []int
	var visit func(int) []int
	visit = func(i int) []int {
		state[i] = visiting
		path = append(path, i)
		for _, d := range b.Parts[i].Depends {
			switch state[d] {
			case visiting:
				at := slices.Index(path, d)
				return append(slices.Clone(path[at:]), d)
			case unseen:
				if c := visit(d); c != nil {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		state[i] = visited
		return nil
	}
	for i := range b.Parts {
		if state[i] == unseen {
			if c := visit(i); c != nil {
				return c
			}
		}
	}
	return nil
}

// partPrompt is what the agent for part i is asked. results and failed are indexed by part;
// only those of the parts i depends on are read, and they have finished.
func (b *Brief) partPrompt(i int, results []string, failed []bool) string {
	var w strings.Builder
	fmt.Fprintf(&w, "You are one of %d agents working on parts of one objective. "+
		"Work on your part only; the other agents handle the others.\n\n", len(b.Parts))
	fmt.Fprintf(&w, "# Objective: %s\n\n", b.Title)
	if b.Body != "" {
		w.WriteString(b.Body + "\n\n")
	}
	if len(b.Parts) > 1 {
		w.WriteString("Other parts, handled by other agents:\n")
		for j, s := range b.Parts {
			if j != i {
				fmt.Fprintf(&w, "- %s\n", s.Title)
			}
		}
		w.WriteString("\n")
	}
	part := b.Parts[i]
	fmt.Fprintf(&w, "## Your part: %s\n\n", part.Title)
	if part.Body != "" {
		w.WriteString(part.Body + "\n\n")
	}
	if len(part.Depends) > 0 {
		w.WriteString("Your part builds on these parts, which have finished. Use their results rather than redoing their work.\n\n")
		for _, d := range part.Depends {
			fmt.Fprintf(&w, "### Result of part: %s\n\n", b.Parts[d].Title)
			if failed[d] {
				w.WriteString("[this part failed; what it returned follows, if anything]\n\n")
			}
			w.WriteString(strings.TrimSpace(results[d]) + "\n\n")
		}
	}
	w.WriteString("End with a complete answer to your part, with file paths and line numbers where they apply, " +
		"and say what you could not find or do.")
	return w.String()
}

// synthesisPrompt asks for the objective from the parts' results; failed[i] is set when part i
// gave no usable result.
func (b *Brief) synthesisPrompt(answers []string, failed []bool) string {
	var w strings.Builder
	fmt.Fprintf(&w, "# Objective: %s\n\n", b.Title)
	if b.Body != "" {
		w.WriteString(b.Body + "\n\n")
	}
	fmt.Fprintf(&w, "This objective was split into %d parts, each worked on by its own agent. Their results follow. "+
		"Address the objective from them: combine them, resolve or state conflicts between them, "+
		"and say what remains open. Do not redo a part's work; use tools only to check a claim that conflicts "+
		"with another part's or that the objective depends on.\n", len(b.Parts))
	for i, s := range b.Parts {
		fmt.Fprintf(&w, "\n## Part %d: %s\n\n", i+1, s.Title)
		if failed[i] {
			w.WriteString("[this part failed; what it returned follows, if anything]\n\n")
		}
		w.WriteString(strings.TrimSpace(answers[i]) + "\n")
	}
	return w.String()
}
