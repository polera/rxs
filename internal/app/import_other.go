//go:build !unix

package app

import "os"

// Non-Unix platforms still check the path and descriptor, but have no portable
// nonblocking open flag to protect against replacement with a blocking source.
const importOpenFlags = os.O_RDONLY
