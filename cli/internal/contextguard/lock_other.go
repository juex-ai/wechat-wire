//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package contextguard

import "fmt"

func acquireFileLock(path string) (func(), error) {
	return nil, fmt.Errorf("context guard file locking is unsupported on this platform")
}
