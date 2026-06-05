// Command loader is the Atlas external schema provider in program mode. It
// registers exactly the Bun model(s) Atlas should introspect and prints the
// resulting DDL to stdout. atlas.hcl runs it via `go run ./loader`.
//
// Program mode (rather than the provider's standalone `load --path` scan) is
// deliberate: the standalone scanner treats every exported struct in a package
// as a table, which would emit phantom tables for the packages' Handler types.
// Listing models explicitly here keeps the generated schema — and therefore the
// migration diff — limited to the real sources of truth.
//
// MULTIPLE MODELS: every Bun model the schema should contain is listed in the
// single Load(...) call below. Adding a model means adding it here — then one
// `atlas migrate diff` regenerates the schema for all of them.
package main

import (
	"fmt"
	"io"
	"os"

	"ariga.io/atlas-provider-bun/bunschema"
	_ "ariga.io/atlas/sdk/recordriver"

	"ba/internal/comment"
	"ba/internal/task"
)

func main() {
	stmts, err := bunschema.New(bunschema.DialectPostgres).Load(
		&task.Task{},
		&comment.Comment{},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load bun schema: %v\n", err)
		os.Exit(1)
	}
	io.WriteString(os.Stdout, stmts)
}
