//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package contextguard

func acquireFileLock(path string) (func(), error) {
	return func() {}, nil
}
