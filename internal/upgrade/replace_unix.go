//go:build !windows

package upgrade

import (
	"bytes"
	"context"
	"fmt"
	"os"
)

func replaceExecutable(ctx context.Context, path string, binary []byte) error {
	tempPath, err := writeReplacement(ctx, path, bytes.NewReader(binary))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
