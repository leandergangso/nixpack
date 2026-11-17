package main

import (
	"fmt"
	"os"

	"github.com/leandergangso/nixpack/internal/nixpack"
)

var version = "dev" // -X main.version="1.0.0"

func main() {
	if err := nixpack.Run(os.Args[1:], version); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
