//go:build !(linux || darwin || freebsd || openbsd || netbsd || dragonfly)

package crunch

import "context"

// lockFile is a no-op where flock isn't available: refreshes are still
// serialised within one process, but not across processes.
func lockFile(context.Context, string) (func(), error) {
	return func() {}, nil
}
