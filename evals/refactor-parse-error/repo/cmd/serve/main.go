// Command serve prints the configuration it would serve with.
package main

import (
	"fmt"
	"os"
	"strings"

	"serve/config"
)

func main() {
	// Settings may also be given as arguments, one "key=value" each.
	c := config.Parse(strings.Join(os.Args[1:], "\n"))
	fmt.Printf("%s on :%d serving %s\n", c.Name, c.Port, strings.Join(c.Dirs, ", "))
}
