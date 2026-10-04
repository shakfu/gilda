package main

import (
	"flag"
	"fmt"
)

func main() {
	flag.Parse()
	name := "world"
	if flag.NArg() > 0 {
		name = flag.Arg(0)
	}
	fmt.Println("Hello, " + name)
}
