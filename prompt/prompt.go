// Package prompt builds the system prompt: the machine, then AGENTS.md files, then skills.
//
// The prompt is built once per session and kept byte-stable, since any change to it
// invalidates every provider's prompt cache.
package prompt

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const base = `You are gilda, a coding agent. Use the tools to inspect and change files. Be terse. State what you did; do not narrate what you are about to do.`

// AgentsFile follows the cross-tool convention at https://agents.md.
const AgentsFile = "AGENTS.md"

const skillsIntro = `A skill is a SKILL.md file of instructions for one kind of task. Each section below gives a skill's path and frontmatter. When a task matches a skill's description, read its SKILL.md before any other action and follow it. Relative paths in it are relative to its directory.`

// maxFile bounds one instruction file, so a stray large file cannot fill the context.
const maxFile = 64 << 10

// maxFrontmatter bounds a skill's frontmatter; the spec's fields total about 1.6 KB.
const maxFrontmatter = 4096

// Options leave parts out of the prompt. Both are sent with every request.
type Options struct {
	NoAgents bool // AGENTS.md files
	NoSkills bool // skills' frontmatter, the user's and the repository's
	// Today is the date the prompt states, as 2006-01-02; empty means the current one.
	Today string
}

// ProjectSkills is where a repository keeps its skills, relative to its root: next to
// AGENTS.md, under the same agent-neutral name.
const ProjectSkills = ".agents/skills"

// Build returns the system prompt for a session rooted at dir. configDir holds the user's own
// AGENTS.md and skills/; it may be empty.
func Build(dir, configDir string, o Options) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n# Environment\n\n")
	fmt.Fprintf(&b, "- Working directory: %s\n", dir)
	fmt.Fprintf(&b, "- Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	// The shell the bash tool runs, not $SHELL: the model writes syntax for this one.
	b.WriteString("- Command shell: bash\n")
	today := o.Today
	if today == "" {
		today = time.Now().Format("2006-01-02")
	}
	fmt.Fprintf(&b, "- Date at session start: %s\n", today)
	if branch := Branch(dir); branch != "" {
		fmt.Fprintf(&b, "- Git branch at session start: %s\n", branch)
	}

	if !o.NoAgents {
		for _, path := range AgentsFiles(dir, configDir) {
			if text := readTrimmed(path); text != "" {
				fmt.Fprintf(&b, "\n# %s\n\n%s\n", path, text)
			}
		}
	}

	if !o.NoSkills {
		var skills []Skill
		if configDir != "" {
			skills = Skills(filepath.Join(configDir, "skills"))
		}
		skills = append(skills, Skills(ProjectSkillsDir(dir))...)
		if len(skills) > 0 {
			fmt.Fprintf(&b, "\n# Skills\n\n%s\n", skillsIntro)
			for _, s := range skills {
				fmt.Fprintf(&b, "\n## %s\n\n%s\n", s.Path, s.Frontmatter)
			}
		}
	}
	return b.String()
}

// AgentsFiles lists the AGENTS.md files that apply to dir: the user's own, then each one from
// the repository root down to dir, so the nearest file comes last and takes precedence. Without
// a repository, only dir's own file applies.
func AgentsFiles(dir, configDir string) []string {
	var out []string
	if configDir != "" {
		if p := filepath.Join(configDir, AgentsFile); isFile(p) {
			out = append(out, p)
		}
	}
	chain := []string{dir}
	if root := RepoRoot(dir); root != "" {
		chain = nil
		for d := dir; ; d = filepath.Dir(d) {
			chain = append(chain, d)
			if d == root {
				break
			}
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if p := filepath.Join(chain[i], AgentsFile); isFile(p) {
			out = append(out, p)
		}
	}
	return out
}

// ProjectSkillsDir is the repository's skills directory for dir: under the repository root, or
// under dir outside a repository.
func ProjectSkillsDir(dir string) string {
	root := RepoRoot(dir)
	if root == "" {
		root = dir
	}
	return filepath.Join(root, ProjectSkills)
}

// Branch names the branch checked out in dir's repository, "detached at" and a short commit
// when none is, or "" outside a repository. It reads .git/HEAD rather than running git.
func Branch(dir string) string {
	root := RepoRoot(dir)
	if root == "" {
		return ""
	}
	gitDir := filepath.Join(root, ".git")
	// A worktree or submodule has a .git file naming its git directory.
	if text := readTrimmed(gitDir); strings.HasPrefix(text, "gitdir: ") {
		gitDir = strings.TrimPrefix(text, "gitdir: ")
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
	}
	head := readTrimmed(filepath.Join(gitDir, "HEAD"))
	if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		return ref
	}
	if len(head) >= 7 && !strings.ContainsAny(head, " \n") {
		return "detached at " + head[:7]
	}
	return ""
}

// RepoRoot returns the nearest ancestor of dir holding .git, or "".
func RepoRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

type Skill struct {
	Path        string
	Frontmatter string
}

// Skills returns every <dir>/<name>/SKILL.md whose frontmatter has a description and fits the
// cap, sorted by path. The frontmatter is passed on unparsed: the model reads YAML, and gilda
// needs no field from it.
func Skills(dir string) []Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		fm, ok := frontmatter(readTrimmed(path))
		if !ok || len(fm) > maxFrontmatter || !hasDescription(fm) {
			continue
		}
		out = append(out, Skill{Path: path, Frontmatter: fm})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func hasDescription(fm string) bool {
	for line := range strings.SplitSeq(fm, "\n") {
		if strings.HasPrefix(line, "description:") {
			return true
		}
	}
	return false
}

// frontmatter returns the text between an opening --- line and the next one.
func frontmatter(text string) (string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", false
	}
	if strings.HasPrefix(rest, "---") {
		return "", false
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", false
	}
	return strings.TrimRight(rest[:end], "\n"), true
}

func readTrimmed(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, maxFile))
	return strings.TrimSpace(string(data))
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
