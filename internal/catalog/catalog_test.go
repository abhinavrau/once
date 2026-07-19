package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolve(t *testing.T) {
	assert.Equal(t, "ghcr.io/basecamp/once-campfire", Resolve("campfire"))
	assert.Equal(t, "ghcr.io/basecamp/fizzy", Resolve("fizzy"))
	assert.Equal(t, "ghcr.io/basecamp/writebook", Resolve("writebook"))

	assert.Equal(t, "ghcr.io/basecamp/once-campfire:latest", Resolve("ghcr.io/basecamp/once-campfire:latest"))
}
