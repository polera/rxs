//go:build windows

package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
)

func replaceExecutable(ctx context.Context, path string, binary []byte) error {
	return replaceExecutableWithRename(ctx, path, binary, os.Rename)
}

func replaceExecutableWithRename(ctx context.Context, path string, binary []byte, rename func(string, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Never delete a previous recovery image, even if direct replacement could
	// succeed. A retained backup must be inspected and moved/removed explicitly.
	backup := path + ".old"
	if _, err := os.Lstat(backup); err == nil {
		return fmt.Errorf("recovery backup %s already exists; inspect and move or remove it before upgrading", backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recovery backup %s: %w", backup, err)
	}
	tempPath, err := writeReplacement(ctx, path, bytes.NewReader(binary))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = rename(tempPath, path); err == nil {
		return nil
	}

	// Windows may not replace a running executable directly, but permits it to
	// be renamed. Keep the old image beside the new one until it can be removed.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = rename(path, backup); err != nil {
		return fmt.Errorf("move current executable aside: %w", err)
	}
	err = ctx.Err()
	if err == nil {
		err = rename(tempPath, path)
	}
	if err != nil {
		installErr := fmt.Errorf("replace %s: %w", path, err)
		// Restoration must run to completion even when installation was canceled.
		if restoreErr := rename(backup, path); restoreErr != nil {
			return errors.Join(installErr, fmt.Errorf("restore %s failed; previous executable remains at %s for manual recovery: %w", path, backup, restoreErr))
		}
		return installErr
	}
	// A running image may remain locked. Leave it for explicit cleanup next time.
	_ = os.Remove(backup)
	return nil
}
