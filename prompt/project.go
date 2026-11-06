package prompt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxProjects bounds the project lines in the environment.
const maxProjects = 6

// Project describes the projects in dir and at its repository root, one line each, with the
// commands likely to build and test them. It reads marker files only, runs nothing, and puts
// no text from them in the prompt beyond names from a fixed list: a cloned repository's files
// are untrusted until its AGENTS.md is.
func Project(dir string) []string {
	dirs := []string{dir}
	if root := RepoRoot(dir); root != "" && root != dir {
		dirs = append(dirs, root)
	}
	var out []string
	for _, d := range dirs {
		for _, p := range projects(d) {
			marker := filepath.Join(d, p.marker)
			if rel, err := filepath.Rel(dir, marker); err == nil {
				marker = rel
			}
			line := fmt.Sprintf("Project: %s (%s)", p.kind, marker)
			if len(p.checks) > 0 {
				line += "; likely checks: `" + strings.Join(p.checks, "`, `") + "`"
			}
			out = append(out, line)
			if len(out) == maxProjects {
				return out
			}
		}
	}
	return out
}

type project struct {
	kind, marker string
	checks       []string
}

func projects(d string) []project {
	// Match directory entries by exact name: on a case-insensitive filesystem (macOS default),
	// os.Stat("makefile") finds Makefile and the prompt would name the wrong file.
	entries, _ := os.ReadDir(d)
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	has := func(name string) bool { return names[name] }
	var out []project
	add := func(kind, marker string, checks ...string) {
		out = append(out, project{kind, marker, checks})
	}
	if has("go.mod") {
		add("Go", "go.mod", "go build ./...", "go vet ./...", "go test ./...")
	}
	if has("Cargo.toml") {
		add("Rust", "Cargo.toml", "cargo build", "cargo clippy", "cargo test")
	}
	if has("package.json") {
		add("JavaScript/TypeScript", "package.json", npmChecks(d, has)...)
	}
	for _, name := range []string{"deno.json", "deno.jsonc"} {
		if has(name) {
			add("Deno", name, "deno lint", "deno test")
			break
		}
	}
	for _, name := range []string{"pyproject.toml", "setup.py", "setup.cfg"} {
		if has(name) {
			add("Python", name, pythonChecks(d, has)...)
			break
		}
	}
	if has("pom.xml") {
		mvn := "mvn"
		if has("mvnw") {
			mvn = "./mvnw"
		}
		add("Java (Maven)", "pom.xml", mvn+" test")
	}
	for _, name := range []string{"build.gradle.kts", "build.gradle"} {
		if has(name) {
			gradle := "gradle"
			if has("gradlew") {
				gradle = "./gradlew"
			}
			add("JVM (Gradle)", name, gradle+" build", gradle+" test")
			break
		}
	}
	if has("Package.swift") {
		add("Swift", "Package.swift", "swift build", "swift test")
	}
	if has("build.zig") {
		add("Zig", "build.zig", "zig build", "zig build test")
	}
	if has("mix.exs") {
		add("Elixir", "mix.exs", "mix compile", "mix test")
	}
	if has("Gemfile") {
		var checks []string
		if has("spec") {
			checks = append(checks, "bundle exec rspec")
		} else if has("Rakefile") {
			checks = append(checks, "bundle exec rake test")
		}
		add("Ruby", "Gemfile", checks...)
	}
	if has("CMakeLists.txt") {
		add("CMake", "CMakeLists.txt")
	}
	for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
		if has(name) {
			var checks []string
			for _, t := range makeTargets(filepath.Join(d, name)) {
				checks = append(checks, "make "+t)
			}
			add("Make", name, checks...)
			break
		}
	}
	return out
}

// knownScripts are the package.json scripts and make targets named in the prompt, in order.
var knownScripts = []string{"build", "check", "typecheck", "lint", "vet", "test"}

func npmChecks(d string, has func(string) bool) []string {
	pm := "npm"
	switch {
	case has("pnpm-lock.yaml"):
		pm = "pnpm"
	case has("yarn.lock"):
		pm = "yarn"
	case has("bun.lock"), has("bun.lockb"):
		pm = "bun"
	}
	var pkg struct {
		Scripts map[string]json.RawMessage `json:"scripts"`
	}
	if json.Unmarshal([]byte(readTrimmed(filepath.Join(d, "package.json"))), &pkg) != nil {
		return nil
	}
	var out []string
	for _, s := range knownScripts {
		if _, ok := pkg.Scripts[s]; ok {
			out = append(out, pm+" run "+s)
		}
	}
	return out
}

func pythonChecks(d string, has func(string) bool) []string {
	pytest := has("pytest.ini") || has("conftest.py") || has("tests") ||
		strings.Contains(readTrimmed(filepath.Join(d, "pyproject.toml")), "[tool.pytest")
	if !pytest {
		return nil
	}
	switch {
	case has("uv.lock"):
		return []string{"uv run pytest"}
	case has("poetry.lock"):
		return []string{"poetry run pytest"}
	}
	return []string{"pytest"}
}

// makeTargets returns the knownScripts that path defines as targets.
func makeTargets(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	defined := map[string]bool{}
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan() && n < 5000; n++ {
		line := sc.Text()
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(rest, "=") || name != strings.TrimSpace(name) {
			continue
		}
		for t := range strings.FieldsSeq(name) {
			defined[t] = true
		}
	}
	var out []string
	for _, t := range knownScripts {
		if defined[t] {
			out = append(out, t)
		}
	}
	return out
}
