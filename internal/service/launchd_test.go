package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderPlist(t *testing.T) {
	withFlag := renderPlist("com.basecamp.once-background", "/usr/local/bin/once", "once", "1")
	assert.Contains(t, withFlag, "<key>EnvironmentVariables</key>")
	assert.Contains(t, withFlag, "<key>ONCE_NO_SELF_UPDATE</key>\n\t\t<string>1</string>")
	assert.Contains(t, withFlag, "</array>\n\t<key>EnvironmentVariables</key>")

	withoutFlag := renderPlist("com.basecamp.once-background", "/usr/local/bin/once", "once", "")
	assert.NotContains(t, withoutFlag, "EnvironmentVariables")
	assert.Contains(t, withoutFlag, "</array>\n\t<key>RunAtLoad</key>")
}
