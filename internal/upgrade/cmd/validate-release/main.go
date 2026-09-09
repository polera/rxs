// Command validate-release applies the updater's version policy to release tags.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/polera/rxs/internal/upgrade"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate-release vMAJOR.MINOR.PATCH")
		os.Exit(1)
	}
	if err := validateTag(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateTag(tag string) error {
	if !strings.HasPrefix(tag, "v") || strings.TrimSpace(tag) != tag {
		return fmt.Errorf("release tag %q must start with lowercase v and contain no surrounding whitespace", tag)
	}
	if _, err := upgrade.Available(tag, tag); err != nil {
		return fmt.Errorf("invalid release tag %q: %w", tag, err)
	}
	return nil
}
