package system

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSameDevice(t *testing.T) {
	dir := t.TempDir()

	same, err := SameDevice(dir, dir)
	require.NoError(t, err)
	assert.True(t, same)

	// A path that does not exist yet is judged by its closest existing
	// ancestor - the device it would be created on.
	same, err = SameDevice(dir, filepath.Join(dir, "not", "created", "yet"))
	require.NoError(t, err)
	assert.True(t, same)
}
