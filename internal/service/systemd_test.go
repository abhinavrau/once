package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderUnit(t *testing.T) {
	withFlag := renderUnit("once", "/usr/local/bin/once", "1")
	assert.Contains(t, withFlag, "ExecStart=/usr/local/bin/once background run --namespace once")
	assert.Contains(t, withFlag, "Environment=ONCE_NO_SELF_UPDATE=1\nRestart=always")

	withoutFlag := renderUnit("once", "/usr/local/bin/once", "")
	assert.NotContains(t, withoutFlag, "Environment=")
	assert.Contains(t, withoutFlag, "ExecStart=/usr/local/bin/once background run --namespace once\nRestart=always")
}
