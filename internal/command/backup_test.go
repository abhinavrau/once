package command

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePasswordDelivery(t *testing.T) {
	assert.NoError(t, validatePasswordDelivery(""))
	assert.NoError(t, validatePasswordDelivery("screen"))

	assert.NoError(t, validatePasswordDelivery("qr"))

	// Whether a QR can actually be drawn here is the custody flow's call, made
	// once the colour profile is known; the flag only refuses unknown words.
	err := validatePasswordDelivery("carrier-pigeon")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "screen, qr")
}

func TestCaptureOffsiteEnvReadsValuesFromEnvironment(t *testing.T) {
	t.Setenv("ONCE_TEST_KEY_ID", "id")
	t.Setenv("ONCE_TEST_SECRET", "secret")

	env, err := captureOffsiteEnv([]string{"ONCE_TEST_KEY_ID", "ONCE_TEST_SECRET"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ONCE_TEST_KEY_ID": "id", "ONCE_TEST_SECRET": "secret"}, env)
}

func TestCaptureOffsiteEnvRejectsUnsetVariable(t *testing.T) {
	_, err := captureOffsiteEnv([]string{"ONCE_TEST_UNSET_VARIABLE"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ONCE_TEST_UNSET_VARIABLE")
}

func TestBackupSubcommandsAreRegistered(t *testing.T) {
	b := newBackupCommand()
	var names []string
	for _, c := range b.cmd.Commands() {
		names = append(names, c.Name())
	}
	assert.ElementsMatch(t, []string{"enable", "disable", "status"}, names)
}
