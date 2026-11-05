package tool

import (
	"fmt"
	"regexp"
	"strings"
)

// A loose match is how edit recovers when old_string is not in the file as given. Models most
// often get whitespace wrong: trailing spaces, or the indentation of a block as a whole. edit
// applies a loose match only when it is unique, and says how it differed. Otherwise its error
// quotes the nearest text, so the next attempt can copy it.

// nearestBudget bounds the line pairs nearest scores by similarity. Past it, nearest counts
// only lines equal but for surrounding whitespace.
const nearestBudget = 50000

// nearestMax bounds the lines an error quotes.
const nearestMax = 20

// linePrefix matches read's line-number prefix.
var linePrefix = regexp.MustCompile(`^\s*\d+\t`)

type looseResult struct {
	start, end int    // byte range of the file replaced
	repl       string // what replaces it
	note       string
}

// fileLine is one line of a file: where it starts, where its text ends before any \r\n or \n,
// and where the next line starts.
type fileLine struct {
	start, end, next int
	text             string
}

func splitFile(text string) []fileLine {
	var out []fileLine
	for start := 0; start < len(text); {
		end, next := len(text), len(text)
		if i := strings.IndexByte(text[start:], '\n'); i >= 0 {
			end, next = start+i, start+i+1
		}
		if end > start && text[end-1] == '\r' {
			end--
		}
		out = append(out, fileLine{start, end, next, text[start:end]})
		start = next
	}
	return out
}

// looseMatch finds old in text as whole lines, first ignoring trailing whitespace, then the
// indentation of the block as a whole. With the second, new is reindented the same way. It
// fails, quoting the nearest text, unless exactly one place matches.
func looseMatch(text, old, new, name string) (looseResult, error) {
	old = strings.ReplaceAll(old, "\r\n", "\n")
	new = strings.ReplaceAll(new, "\r\n", "\n")
	body := strings.TrimSuffix(old, "\n")
	withEOL := body != old
	want := strings.Split(body, "\n")
	lines := splitFile(text)

	if prefixed(want) {
		return looseResult{}, fmt.Errorf("old_string not found in %s: it starts with read's line-number prefixes; remove them", name)
	}
	if strings.TrimSpace(body) != "" {
		for _, m := range []struct {
			match func(lines []fileLine, at int, want []string) (fileIndent, oldIndent string, ok bool)
			how   string
		}{
			{trailing, "ignoring trailing whitespace"},
			{indented, "ignoring the block's indentation"},
		} {
			var hits []int
			var fileIndent, oldIndent string
			for at := 0; at+len(want) <= len(lines); at++ {
				if f, o, ok := m.match(lines, at, want); ok {
					hits = append(hits, at)
					fileIndent, oldIndent = f, o
				}
			}
			switch {
			case len(hits) > 1:
				return looseResult{}, fmt.Errorf("old_string not found in %s as given. %s, it matches %d places (lines %s); copy it exactly, with more context",
					name, strings.ToUpper(m.how[:1])+m.how[1:], len(hits), lineList(hits))
			case len(hits) == 0:
				continue
			}
			repl, ok := reindent(new, oldIndent, fileIndent)
			if !ok {
				return looseResult{}, fmt.Errorf("old_string not found in %s as given. It matches lines %d-%d ignoring indentation, "+
					"but new_string is not indented like old_string; resend both with the file's indentation:\n%s",
					name, hits[0]+1, hits[0]+len(want), quote(lines, hits[0], len(want)))
			}
			if strings.Contains(text, "\r\n") {
				repl = strings.ReplaceAll(repl, "\n", "\r\n")
			}
			last := lines[hits[0]+len(want)-1]
			r := looseResult{start: lines[hits[0]].start, end: last.end, repl: repl}
			if withEOL {
				r.end = last.next
			}
			r.note = "matched " + m.how
			if fileIndent != oldIndent {
				r.note += "; new_string reindented to match"
			}
			return r, nil
		}
	}
	if at, ok := nearest(lines, want); ok {
		return looseResult{}, fmt.Errorf("old_string not found in %s. The nearest text is at lines %d-%d:\n%s\nCopy old_string exactly from the file.",
			name, at+1, at+len(want), quote(lines, at, len(want)))
	}
	return looseResult{}, fmt.Errorf("old_string not found in %s; copy it exactly from the file, without read's line-number prefixes", name)
}

func prefixed(want []string) bool {
	for _, w := range want {
		if w != "" && !linePrefix.MatchString(w) {
			return false
		}
	}
	return strings.Join(want, "") != ""
}

// trailing matches lines equal but for trailing whitespace.
func trailing(lines []fileLine, at int, want []string) (string, string, bool) {
	for j, w := range want {
		if strings.TrimRight(lines[at+j].text, " \t") != strings.TrimRight(w, " \t") {
			return "", "", false
		}
	}
	return "", "", true
}

// indented matches lines equal once each side's common indentation is removed, and returns
// both indentations.
func indented(lines []fileLine, at int, want []string) (string, string, bool) {
	got := make([]string, len(want))
	for j := range want {
		got[j] = lines[at+j].text
		if strings.TrimSpace(got[j]) != strings.TrimSpace(want[j]) {
			return "", "", false
		}
	}
	fi, oi := commonIndent(got), commonIndent(want)
	for j, w := range want {
		if strings.TrimSpace(w) == "" {
			continue
		}
		if strings.TrimRight(got[j][len(fi):], " \t") != strings.TrimRight(w[len(oi):], " \t") {
			return "", "", false
		}
	}
	return fi, oi, true
}

func commonIndent(lines []string) string {
	var out string
	first := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		ind := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if first {
			out, first = ind, false
			continue
		}
		n := 0
		for n < len(out) && n < len(ind) && out[n] == ind[n] {
			n++
		}
		out = out[:n]
	}
	return out
}

// reindent replaces the from prefix of each of new's lines with to. It fails if a non-blank
// line lacks from.
func reindent(new, from, to string) (string, bool) {
	if from == to {
		return new, true
	}
	lines := strings.Split(new, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		rest, ok := strings.CutPrefix(l, from)
		if !ok {
			return "", false
		}
		lines[i] = to + rest
	}
	return strings.Join(lines, "\n"), true
}

// nearest returns the start of the run of len(want) lines most like want, if at least half of
// it is alike.
func nearest(lines []fileLine, want []string) (int, bool) {
	k := len(want)
	if k == 0 || k > len(lines) {
		return 0, false
	}
	exact := (len(lines)-k+1)*k > nearestBudget
	trimmed := make([]string, k)
	for j, w := range want {
		trimmed[j] = strings.TrimSpace(w)
	}
	best, bestAt := 0.0, -1
	for at := 0; at+k <= len(lines); at++ {
		score := 0.0
		for j := range k {
			got := strings.TrimSpace(lines[at+j].text)
			switch {
			case got == trimmed[j]:
				score++
			case !exact:
				score += dice(got, trimmed[j])
			}
		}
		if score > best {
			best, bestAt = score, at
		}
	}
	return bestAt, bestAt >= 0 && best >= float64(k)/2
}

// dice is the Sørensen-Dice coefficient of a's and b's byte bigrams.
func dice(a, b string) float64 {
	if len(a) < 2 || len(b) < 2 {
		return 0
	}
	counts := make(map[uint16]int, len(a))
	for i := 0; i+1 < len(a); i++ {
		counts[uint16(a[i])<<8|uint16(a[i+1])]++
	}
	common := 0
	for i := 0; i+1 < len(b); i++ {
		if k := uint16(b[i])<<8 | uint16(b[i+1]); counts[k] > 0 {
			counts[k]--
			common++
		}
	}
	return 2 * float64(common) / float64(len(a)-1+len(b)-1)
}

// quote shows n lines from at as read numbers them, up to nearestMax.
func quote(lines []fileLine, at, n int) string {
	var b strings.Builder
	for i := at; i < at+n && i < len(lines); i++ {
		if i-at == nearestMax {
			fmt.Fprintf(&b, "... %d more lines\n", n-nearestMax)
			break
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i].text)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func lineList(hits []int) string {
	var parts []string
	for i, h := range hits {
		if i == 5 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, fmt.Sprint(h+1))
	}
	return strings.Join(parts, ", ")
}
