// Command gilda is a coding agent for the terminal.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/shakfu/gilda/app"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/provider"
	"github.com/shakfu/gilda/tui"
)

var version = "0.1.0"

type flags struct {
	app.Options
	prompt string
	// headless is set by an explicit -p, even one whose stdin turns out empty.
	headless bool
	json     bool
	noColor  bool
	sessions bool
}

// exitError carries an exit status out of cobra's RunE.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func main() {
	var f flags
	cmd := &cobra.Command{
		Use:   "gilda",
		Short: "A coding agent for the terminal",
		Long: "gilda is a coding agent. Without -p it starts a REPL; with -p it answers one\n" +
			"prompt and exits.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f.headless = cmd.Flags().Changed("prompt")
			if !cmd.Flags().Changed("api-key") {
				f.APIKey = os.Getenv("GILDA_API_KEY")
			} else if !f.json {
				fmt.Fprintln(os.Stderr, "gilda: warning: --api-key shows the key in the process "+
					"list and shell history; set GILDA_API_KEY instead")
			}
			if f.json && !f.headless {
				return errors.New("--json needs -p")
			}
			if f.Permissions != "" {
				if _, err := permission.Parse(f.Permissions); err != nil {
					return err
				}
			}
			if f.Effort != "" {
				if err := checkEffort(f.Effort); err != nil {
					return err
				}
			}
			if code := run(f); code != 0 {
				return exitError(code)
			}
			return nil
		},
	}
	cmd.SetVersionTemplate("gilda {{.Version}}\n")
	fl := cmd.Flags()
	// A backquoted word in a usage names the flag's value in --help.
	fl.StringVarP(&f.Provider, "provider", "P", os.Getenv("GILDA_PROVIDER"),
		"`ID`: "+strings.Join(provider.IDs(), ", "))
	fl.StringVarP(&f.Model, "model", "m", os.Getenv("GILDA_MODEL"), "model `ID`, or PROVIDER:ID")
	fl.StringVarP(&f.prompt, "prompt", "p", "", "answer one `PROMPT` and exit; - reads stdin")
	fl.StringVarP(&f.Root, "root", "C", "", "working `DIR` (default: the current one)")
	fl.StringVar(&f.BaseURL, "base-url", os.Getenv("GILDA_BASE_URL"),
		"provider endpoint `URL`; needs --provider; turns off cost estimates")
	// Not defaulted from GILDA_API_KEY, which --help would print; RunE reads it instead.
	fl.StringVar(&f.APIKey, "api-key", "", "provider `KEY`; needs --provider; prefer GILDA_API_KEY")
	fl.StringVar(&f.Permissions, "permissions", os.Getenv("GILDA_PERMISSIONS"),
		"`MODE` for what runs without asking: auto, ask, all, read-only "+
			"(default: mode in settings.toml, else auto). auto asks before "+
			"touching secrets and writing outside the root or to protected paths")
	fl.StringVar(&f.Effort, "effort", "", "reasoning `LEVEL`: low, medium, high, xhigh, max")
	fl.StringVar(&f.Mock, "mock", "", "replay a scripted JSON conversation from `FILE`")
	fl.Int64Var(&f.MaxTokens, "max-tokens", 0, "cap each response at `N` output tokens "+
		"(default: settings.toml, else 32000)")
	fl.IntVar(&f.MaxTurns, "max-turns", 0, "allow `N` provider round-trips per prompt "+
		"(default: settings.toml, else 64)")
	fl.Int64Var(&f.Context, "context", 0, "context window in `TOKENS` "+
		"(default: settings.toml, else from the model list)")
	fl.BoolVar(&f.json, "json", false, "with -p: print JSON lines, ending in a result record")
	fl.BoolVar(&f.noColor, "no-color", false, "disable colour; also off when NO_COLOR is set")
	fl.StringSliceVar(&f.Only, "tools", nil, "offer only these tool `NAMES`, such as read,bash (default: all)")
	fl.StringVar(&f.AppendSystem, "append-system", "", "add `TEXT` to the end of the system prompt")
	fl.BoolVarP(&f.Continue, "continue", "c", false, "resume the newest session saved for the working directory")
	fl.StringVar(&f.Resume, "resume", "", "resume the session with this `ID`, or a unique prefix of it")
	fl.BoolVar(&f.sessions, "sessions", false, "list the sessions saved for the working directory and exit")
	fl.BoolVar(&f.Refresh, "refresh-models", false, "refetch the price list, ignoring the cache")
	fl.SortFlags = false
	cmd.Flags().BoolP("help", "h", false, "print this help")
	cmd.Flags().BoolP("version", "v", false, "print the version")
	cobra.AddTemplateFunc("wrappedFlags", func(fs *pflag.FlagSet) string {
		return strings.TrimRight(fs.FlagUsagesWrapped(helpWidth()), "\n")
	})
	cmd.SetUsageTemplate(usage)

	err := cmd.Execute()
	var code exitError
	switch {
	case errors.As(err, &code):
		os.Exit(int(code))
	case err != nil:
		fmt.Fprintln(os.Stderr, "gilda:", err)
		os.Exit(2)
	}
}

func run(f flags) int {
	if f.Root != "" {
		if err := os.Chdir(f.Root); err != nil {
			fmt.Fprintln(os.Stderr, "gilda:", err)
			return 2
		}
		f.Root, _ = os.Getwd()
	}
	if f.sessions {
		return listSessions(os.Stdout, f.Root)
	}
	if f.prompt == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gilda:", err)
			return 1
		}
		f.prompt = string(data)
	}
	if f.headless && strings.TrimSpace(f.prompt) == "" {
		err := errors.New("the prompt is empty")
		if f.json {
			writeFailure(os.Stdout, err)
			return 2
		}
		fmt.Fprintln(os.Stderr, "gilda:", err)
		return 2
	}

	if path := os.Getenv("GILDA_LOG"); path != "" {
		logFile, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gilda:", err)
			return 2
		}
		defer logFile.Close()
		llm.SetLog(logFile)
	}

	a, err := app.New(f.Options)
	if err != nil {
		if f.json {
			return writeFailure(os.Stdout, err)
		}
		fmt.Fprintln(os.Stderr, "gilda:", err)
		return 1
	}
	defer a.Close()

	// SIGHUP and SIGTERM cancel the run instead of exiting on the spot, so the REPL restores the
	// terminal and background jobs die through the deferred Close.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var got atomic.Int32
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		if s, ok := <-sigs; ok {
			got.Store(int32(s.(syscall.Signal)))
			stop()
		}
	}()
	bySignal := func(code int) int {
		if s := got.Load(); s != 0 {
			return 128 + int(s)
		}
		return code
	}

	color := !f.noColor && os.Getenv("NO_COLOR") == ""
	if f.headless {
		return bySignal(headless(ctx, a, f.prompt, f.json, color))
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "gilda: the REPL needs a terminal; pass -p for a one-shot run")
		return 2
	}
	if err := tui.Run(ctx, a, tui.Options{Version: version, Color: color}); err != nil && got.Load() == 0 {
		fmt.Fprintln(os.Stderr, "gilda:", err)
		return 1
	}
	return bySignal(0)
}

// usage is cobra's template for a command without subcommands, with flag help wrapped to
// the terminal.
const usage = `Usage:
  {{.UseLine}}

Flags:
{{wrappedFlags .LocalFlags}}
`

// helpWidth is 80 columns, or the terminal's width when narrower.
func helpWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && w < 80 {
		return w
	}
	return 80
}

func checkEffort(e string) error {
	switch e {
	case "low", "medium", "high", "xhigh", "max":
		return nil
	}
	return fmt.Errorf("--effort must be low, medium, high, xhigh or max")
}

// listSessions prints the sessions saved for root, newest first.
func listSessions(w io.Writer, root string) int {
	if root == "" {
		root, _ = os.Getwd()
	}
	list, err := app.Sessions("", root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gilda:", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "gilda: no saved sessions for", root)
		return 0
	}
	for _, s := range list {
		fmt.Fprintf(w, "%s  %s  %3d msgs  %s/%s  %s\n", s.ID, s.Updated.Local().Format("2006-01-02 15:04"),
			s.Messages, s.Provider, s.Model, s.Title)
	}
	return 0
}
