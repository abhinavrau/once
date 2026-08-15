package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/basecamp/once/internal/docker"
)

func TestResolveReturnsCatalogEntry(t *testing.T) {
	campfire := Resolve("campfire")
	assert.Equal(t, "ghcr.io/basecamp/once-campfire", campfire.ImageRef)
	assert.Equal(t, "campfire", campfire.Profile.BaseName)

	assert.Equal(t, "ghcr.io/basecamp/fizzy", Resolve("fizzy").ImageRef)
	assert.Equal(t, "ghcr.io/basecamp/writebook", Resolve("writebook").ImageRef)
}

func TestResolveUnmatchedIsBareImageRef(t *testing.T) {
	entry := Resolve("ghcr.io/basecamp/once-campfire:latest")
	assert.Equal(t, App{ImageRef: "ghcr.io/basecamp/once-campfire:latest"}, entry)
}

func TestResolveVaultwarden(t *testing.T) {
	entry := Resolve("vaultwarden")
	assert.Equal(t, "vaultwarden/server", entry.ImageRef)
	assert.Equal(t, docker.AppProfile{
		BaseName:   "vaultwarden",
		DataPath:   "/data",
		HealthPath: "/alive",
		URLEnvVar:  "DOMAIN",
	}, entry.Profile)
}

func TestEveryEntrySetsBaseNameFromAlias(t *testing.T) {
	for _, app := range Apps {
		assert.Equal(t, app.Alias, app.Profile.BaseName, "entry %s", app.Name)
	}
}
