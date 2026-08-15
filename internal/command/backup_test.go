package command

import (
	"errors"
	"testing"
	"time"

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

// The staleness signal is the only surface a failing offsite leg gets: the
// run succeeds regardless, so status has to say how old the offsite copy is,
// including when the repo cannot be asked at all.
func TestDescribeOffsiteAge(t *testing.T) {
	assert.Equal(t, "offsite copy is 3 days old",
		describeOffsiteAge(time.Now().Add(-73*time.Hour), nil))
	assert.Equal(t, "offsite copy is 6 hours old",
		describeOffsiteAge(time.Now().Add(-6*time.Hour), nil))
	assert.Equal(t, "offsite copy is less than an hour old",
		describeOffsiteAge(time.Now().Add(-time.Minute), nil))
	assert.Equal(t, "no offsite copy yet", describeOffsiteAge(time.Time{}, nil))
	assert.Contains(t, describeOffsiteAge(time.Time{}, errors.New("unreachable")), "unknown: unreachable")
}

func TestBackupSubcommandsAreRegistered(t *testing.T) {
	b := newBackupCommand()
	var names []string
	for _, c := range b.cmd.Commands() {
		names = append(names, c.Name())
	}
	assert.ElementsMatch(t, []string{"enable", "disable", "status"}, names)
}
