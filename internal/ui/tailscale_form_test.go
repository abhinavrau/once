package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTailscaleForm_RequiresCredentialsWhenEnabling(t *testing.T) {
	m := TailscaleForm{progress: NewProgress(0, Colors.Border)}

	c, _ := m.Update(tailscaleFormSubmitMsg{enable: true})

	got := c.(TailscaleForm)
	assert.Error(t, got.err)
	assert.False(t, got.running)
}

func TestTailscaleForm_DisableNeedsNoCredentials(t *testing.T) {
	m := TailscaleForm{progress: NewProgress(0, Colors.Border)}

	c, cmd := m.Update(tailscaleFormSubmitMsg{enable: false})

	got := c.(TailscaleForm)
	assert.True(t, got.running)
	assert.NoError(t, got.err)
	assert.NotNil(t, cmd)
}
