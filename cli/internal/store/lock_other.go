//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package store

func acquireUserBookLock(path string) (func(), error) {
	return func() {}, nil
}
