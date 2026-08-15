package system

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLocked reports that another process holds the lock.
var ErrLocked = errors.New("lock is held by another process")

// FileLock is a host-wide advisory lock backed by flock(2). It guards
// cross-process critical sections - the scheduled Backup Run and a manual
// export both pause App Containers, and two pausers racing produce torn
// SQLite copies that look like backups.
type FileLock struct {
	file *os.File
}

// TryLock takes the lock non-blocking: ErrLocked when it is already held.
// The lock file is created world-writable so the daemon (root) and a
// non-root CLI can share it; the file itself carries no data.
func TryLock(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return &FileLock{file: file}, nil
}

// Unlock releases the lock; calling it again is a no-op.
func (l *FileLock) Unlock() {
	if l.file == nil {
		return
	}
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	l.file.Close()
	l.file = nil
}
