package main

import (
	"fmt"
	"os"
	"time"

	"github.com/mbalazy/pm-cli/internal/cmd"
	"github.com/mbalazy/pm-cli/internal/tui/board"
)

func init() {
	board.ProcessStart = time.Now()
}

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
