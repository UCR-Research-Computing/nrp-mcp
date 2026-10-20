// Package filelock holds an exclusive lock on a file across processes, so two nrp-mcp
// servers (two AI apps open at once) never lose each other's token or approval updates.
package filelock

import (
	"os"
)

// Lock is a held lock; Unlock releases it.
type Lock struct{ f *os.File }

// Acquire blocks until it holds an exclusive lock on path (created 0600 if missing).
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lock(f); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Unlock releases the lock.
func (l *Lock) Unlock() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlock(l.f)
	_ = l.f.Close()
}
