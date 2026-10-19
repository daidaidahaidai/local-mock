//go:build linux || darwin

package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

type Locker struct{ Root string }

var idRE = regexp.MustCompile(`^[0-9a-f]{24}$`)

func (l Locker) Acquire(id string) (func(), error) {
	if !idRE.MatchString(id) {
		return nil, fmt.Errorf("invalid run ID")
	}
	dir := filepath.Join(l.Root, "locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("run %s has an active operation", id)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
