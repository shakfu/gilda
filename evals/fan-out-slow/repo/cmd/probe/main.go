// Command probe asks one service for its live retry limit. Each call takes about 20 seconds.
package main

import (
	"fmt"
	"os"
	"time"
)

// seeds give each service's limit as (seed * 7) % 13, so the values are not literal in the
// binary.
var seeds = map[string]int{"billing": 5, "auth": 8, "search": 3, "mail": 10}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: probe SERVICE")
		os.Exit(2)
	}
	seed, ok := seeds[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "probe: unknown service %q; the services are billing, auth, search and mail\n", os.Args[1])
		os.Exit(2)
	}
	time.Sleep(20 * time.Second)
	fmt.Printf("%s: max retries %d\n", os.Args[1], seed*7%13)
}
