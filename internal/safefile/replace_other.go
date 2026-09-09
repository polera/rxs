//go:build !windows

package safefile

import "os"

func replace(source, destination string) error {
	return os.Rename(source, destination)
}
