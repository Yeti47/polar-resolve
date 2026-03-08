package main

import (
	"fmt"
	"os"

	"github.com/Yeti47/polar-resolve/cmd/polar-resolve/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
