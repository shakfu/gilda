// Package shell reads the programs a bash command runs, well enough to count tool use in
// gilda's --json output. It is shared by scripts/tally and scripts/eval.
package shell

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// ToolName is the tool of a tool_result record, or for bash "bash " plus the programs the
// command runs, joined by "+". A subagent's call, labelled "[task] ", is prefixed "task>".
func ToolName(name, label string) string {
	prefix := ""
	if rest, ok := strings.CutPrefix(label, "[task] "); ok {
		prefix, label = "task>", rest
	}
	if name != "bash" {
		return prefix + name
	}
	return prefix + "bash " + strings.Join(Programs(strings.TrimPrefix(label, "$ ")), "+")
}

var (
	assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	// redirection matches a word that opens a redirection, such as "<", "2>" or ">out"; a bare
	// operator takes the next word as its target.
	redirection     = regexp.MustCompile(`^[0-9]*(&>|<|>)`)
	bareRedirection = regexp.MustCompile(`^[0-9]*(&>|<<?|>>?)&?$`)
	// skipWord precedes the program in a simple command: keywords, negation, and wrappers that
	// run the next word.
	skipWord = set("if", "then", "else", "elif", "while", "until", "do", "done", "fi", "esac",
		"!", "{", "}", "time", "env", "nohup", "sudo", "exec", "command")
	// header starts a simple command that runs nothing itself, such as "for f in a b".
	header = set("for", "select", "case", "function")
	// builtin names a shell builtin that only prints or sets state. It counts only when a
	// command runs nothing else, as in "echo hi".
	builtin = set("cd", "echo", "printf", "true", "false", ":", "export", "set", "unset", "local",
		"read", "shift", "test", "[", "[[", "return", "exit", "wait", "source", ".", "trap")
)

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// Programs returns the programs a command runs, sorted and unique: the first word of each
// simple command, after keywords, assignments and wrappers, without builtins. It is a sketch
// of shell parsing, enough to see through loops, chains and pipelines, not a parser.
func Programs(cmd string) []string {
	found := map[string]bool{}
	firstBuiltin := ""
	for _, words := range simpleCommands(cmd) {
	scan:
		for i := 0; i < len(words); i++ {
			w := words[i]
			switch {
			case skipWord[w] || assignment.MatchString(w):
				continue
			case redirection.MatchString(w):
				if bareRedirection.MatchString(w) {
					i++
				}
				continue
			case header[w]:
				break scan
			case builtin[w]:
				if firstBuiltin == "" {
					firstBuiltin = w
				}
				break scan
			default:
				found[path.Base(w)] = true
				break scan
			}
		}
	}
	if len(found) == 0 {
		if firstBuiltin != "" {
			return []string{firstBuiltin}
		}
		return []string{"(empty)"}
	}
	out := make([]string, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// simpleCommands splits cmd into words, grouped into simple commands at unquoted ; & | ( )
// and newlines. Quotes are removed, and heredoc bodies are skipped, so neither a quoted
// separator nor a line of a heredoc starts a command.
func simpleCommands(cmd string) [][]string {
	var (
		cmds        [][]string
		words       []string
		word        strings.Builder
		inWord      bool
		quote       rune
		heredoc     string // delimiter of a heredoc whose body starts at the next newline
		wantHeredoc bool   // the next word is a heredoc delimiter
	)
	endWord := func() {
		if !inWord {
			return
		}
		w := word.String()
		word.Reset()
		inWord = false
		switch {
		case wantHeredoc:
			heredoc, wantHeredoc = w, false
		case w == "<<" || w == "<<-":
			wantHeredoc = true
		case strings.HasPrefix(w, "<<"):
			heredoc = strings.TrimPrefix(strings.TrimPrefix(w, "<<"), "-")
		default:
			words = append(words, w)
		}
	}
	endCmd := func() {
		endWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
		}
		words = nil
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && i+1 < len(rs) {
				i++
				word.WriteRune(rs[i])
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == '\\' && i+1 < len(rs):
			i++
			word.WriteRune(rs[i])
			inWord = true
		case r == '\n':
			endCmd()
			if heredoc != "" {
				// Skip the body up to the line holding only the delimiter.
				rest := string(rs[i+1:])
				skip := len(rest)
				for off := 0; off < len(rest); {
					line, _, _ := strings.Cut(rest[off:], "\n")
					if strings.TrimSpace(line) == heredoc {
						skip = off + len(line)
						break
					}
					off += len(line) + 1
				}
				i += len([]rune(rest[:skip]))
				heredoc = ""
			}
		case redirect(rs, i):
			word.WriteRune(r)
			inWord = true
		case r == ';' || r == '&' || r == '|' || r == '(' || r == ')':
			endCmd()
		case r == ' ' || r == '\t':
			endWord()
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	endCmd()
	return cmds
}

// redirect reports whether the & or | at rs[i] belongs to a redirection such as 2>&1, &> or
// >|, where it separates nothing.
func redirect(rs []rune, i int) bool {
	r := rs[i]
	prev := i > 0 && (rs[i-1] == '>' || rs[i-1] == '<')
	next := i+1 < len(rs) && rs[i+1] == '>'
	return r == '&' && (prev || next) || r == '|' && i > 0 && rs[i-1] == '>'
}
