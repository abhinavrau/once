package system

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once", "backup.lock")

	lock, err := TryLock(path)
	require.NoError(t, err)

	_, err = TryLock(path)
	assert.ErrorIs(t, err, ErrLocked)

	lock.Unlock()

	lock2, err := TryLock(path)
	require.NoError(t, err)
	lock2.Unlock()
}

func TestFreeDisk(t *testing.T) {
	free, err := FreeDisk(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, free)
}
