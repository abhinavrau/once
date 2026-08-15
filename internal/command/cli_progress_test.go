package command

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func updateCLIProgress(t *testing.T, m *cliProgress, p docker.DeployProgress) {
	t.Helper()
	_, cmd := m.Update(cliProgressUpdateMsg{p: p})
	require.NotNil(t, cmd, "the wait command is re-armed after every report")
}

// The offsite backup leg is a measured send, so the CLI gives it the bar and
// the status names what is going out - "downloading" would be a lie.
func TestCLIProgressRendersAnUpload(t *testing.T) {
	m := newCLIProgress("Validating backup repositories", nil)

	updateCLIProgress(t, m, docker.DeployProgress{Stage: docker.DeployStageWaiting, Status: "creating offsite"})
	assert.Equal(t, "creating offsite", m.stage)
	assert.Equal(t, -1, m.progress.Percent())

	updateCLIProgress(t, m, docker.DeployProgress{Stage: docker.DeployStageUploading, Percentage: 42, Status: "uploading offsite"})
	assert.Equal(t, "uploading offsite", m.stage)
	assert.Equal(t, 42, m.progress.Percent())
	assert.Contains(t, m.View().Content, "Validating backup repositories uploading offsite: ")
}
