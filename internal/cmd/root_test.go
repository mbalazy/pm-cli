package cmd

import "testing"

// `pm finish` has to be reachable from the root command - a subcommand nobody
// registered is a subcommand nobody can run.
func TestFinishIsRegisteredOnRoot(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "finish" {
			return
		}
	}
	t.Fatal("`finish` is not registered on the root command")
}
