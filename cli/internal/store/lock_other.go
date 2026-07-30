//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package store

import "fmt"

func acquireUserBookLock(path string) (func(), error) {
	return nil, fmt.Errorf("user book file locking is unsupported on this platform")
}
