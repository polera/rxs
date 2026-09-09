package app

import (
	"context"
	"errors"
	"io"
	"os"
)

// Imports accept regular files (including symlinks to them), not pipes or
// devices. Check both the path and the opened descriptor: on Unix O_NONBLOCK
// prevents a substituted FIFO from blocking open before we can reject it.
// Filesystem metadata/open/read calls themselves are not context-interruptible;
// this is not a hard deadline for stalled network filesystems or device drivers.
func openImportFile(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("OPML source must be a regular file")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// #nosec G304 -- The user chooses the import path; it is not confined to a data directory.
	file, err := os.OpenFile(path, importOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, err
	}
	after, err := file.Stat()
	if err == nil && (!after.Mode().IsRegular() || !os.SameFile(before, after)) {
		err = errors.New("OPML source changed while opening; expected the same regular file")
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

type importReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r importReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(buffer[:min(len(buffer), 32<<10)])
	if canceled := r.ctx.Err(); canceled != nil {
		return 0, canceled
	}
	return n, err
}
