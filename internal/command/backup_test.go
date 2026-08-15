package command

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePasswordDelivery(t *testing.T) {
	assert.NoError(t, validatePasswordDelivery(""))
	assert.NoError(t, validatePasswordDelivery("screen"))

	// qr ships with the phone handoff; until then it is a named refusal, not
	// an unknown value.
	err := validatePasswordDelivery("qr")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available yet")

	assert.Error(t, validatePasswordDelivery("carrier-pigeon"))
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
