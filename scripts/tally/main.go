// Command tally counts tool use in gilda's --json output, per model: calls, failures and output
// bytes for each tool, with bash split by the programs it ran. See docs/dev/tools.md.
//
//	gilda -p "task" --json > run.jsonl
//	go run ./scripts/tally run.jsonl [more.jsonl ...]
//
// With no files it reads stdin. A run's records are attributed to the model in the result
// record that ends it.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/shakfu/gilda/scripts/internal/shell"
)

type record struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Label    string `json:"label"`
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type key struct{ model, tool string }

type count struct{ calls, failed, bytes int }

func main() {
	var inputs []io.Reader
	for _, name := range os.Args[1:] {
		f, err := os.Open(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		inputs = append(inputs, f)
	}
	if len(inputs) == 0 {
		inputs = []io.Reader{os.Stdin}
	}
	totals := map[key]*count{}
	for _, r := range inputs {
		if err := tally(r, totals); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	report(os.Stdout, totals)
}

// tally adds r's tool results to totals. Results are held until the run's result record names
// the model; a run cut off before it counts under "unknown".
func tally(r io.Reader, totals map[key]*count) error {
	var pending []record
	flush := func(model string) {
		for _, rec := range pending {
			k := key{model, shell.ToolName(rec.Name, rec.Label)}
			c := totals[k]
			if c == nil {
				c = &count{}
				totals[k] = c
			}
			c.calls++
			if !rec.OK {
				c.failed++
			}
			c.bytes += len(rec.Output)
		}
		pending = nil
	}
	sc := bufio.NewScanner(r)
	// A record carries a whole tool result, up to the output cap plus JSON escaping.
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for n := 1; sc.Scan(); n++ {
		var rec record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		switch rec.Type {
		case "tool_result":
			pending = append(pending, rec)
		case "result":
			flush(rec.Provider + "/" + rec.Model)
		}
	}
	flush("unknown")
	return sc.Err()
}

func report(w io.Writer, totals map[key]*count) {
	byModel := map[string]int{}
	var keys []key
	for k, c := range totals {
		keys = append(keys, k)
		byModel[k.model] += c.bytes
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.model != b.model {
			return a.model < b.model
		}
		if totals[a].bytes != totals[b].bytes {
			return totals[a].bytes > totals[b].bytes
		}
		return a.tool < b.tool
	})
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "model\ttool\tcalls\tfailed\toutput bytes\tshare\t")
	for _, k := range keys {
		c := totals[k]
		share := 0
		if t := byModel[k.model]; t > 0 {
			share = c.bytes * 100 / t
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d%%\t\n", k.model, k.tool, c.calls, c.failed, c.bytes, share)
	}
	tw.Flush()
}
