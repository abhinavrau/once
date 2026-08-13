package command

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/basecamp/once/internal/docker"
)

func TestResolvableTailnetURLsSkipsStoppedAndNonTailnetApps(t *testing.T) {
	app := func(name string, exposure docker.Exposure, running bool) *docker.Application {
		return &docker.Application{
			Settings: docker.ApplicationSettings{Name: name, Host: name, Exposure: exposure},
			Running:  running,
		}
	}
	apps := []*docker.Application{
		app("writebook", docker.ExposureTailnet, true),
		app("campfire", docker.ExposureTailnet, false),
		app("fizzy", docker.ExposureLocal, true),
	}
	tailnet := map[string]string{
		"writebook": "https://writebook.tail1234.ts.net",
		"campfire":  "https://campfire.tail1234.ts.net",
	}

	assert.Equal(t, []string{"https://writebook.tail1234.ts.net", "", ""}, resolvableTailnetURLs(apps, tailnet))
}
