//go:build unix

package app

import (
	"os"
	"syscall"
)

const importOpenFlags = os.O_RDONLY | syscall.O_NONBLOCK
