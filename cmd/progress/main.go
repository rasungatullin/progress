package main

import (
	"fmt"
	"os"

	"github.com/rasungatullin/progress/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
