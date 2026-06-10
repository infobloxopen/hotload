// Command depbudget reads `go mod edit -json` output on stdin and prints
// the module's direct (non-indirect) requirements, one per line. The
// Makefile's dep-budget target uses it to assert that the hotload core's
// only direct dependency is fsnotify.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

type goMod struct {
	Require []struct {
		Path     string
		Indirect bool
	}
}

func main() {
	var mod goMod
	if err := json.NewDecoder(os.Stdin).Decode(&mod); err != nil {
		log.Fatalf("depbudget: decoding go mod json: %v", err)
	}
	for _, req := range mod.Require {
		if !req.Indirect {
			fmt.Println(req.Path)
		}
	}
}
