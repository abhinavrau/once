package command

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatRepoPassword(t *testing.T) {
	assert.Equal(t, "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
		formatRepoPassword("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"))
}

func TestBackupSubcommandsAreRegistered(t *testing.T) {
	b := newBackupCommand()
	var names []string
	for _, c := range b.cmd.Commands() {
		names = append(names, c.Name())
	}
	assert.ElementsMatch(t, []string{"enable", "disable", "status"}, names)
}
