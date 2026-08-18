package command

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/basecamp/once/internal/docker"
)

func TestTailnetAndFunnelURLForSkipWhenDisabledOrNotTailnet(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	settings := docker.TailscaleSettings{Topology: docker.TailnetTopologyNodes, DomainSuffix: "tail1234.ts.net"}
	tailnetApp := &docker.Application{Settings: docker.ApplicationSettings{Host: "writebook", Exposure: docker.ExposureTailnet, FunnelExpiresAt: &expiresAt}}
	localApp := &docker.Application{Settings: docker.ApplicationSettings{Host: "campfire", Exposure: docker.ExposureLocal}}

	assert.Equal(t, "https://writebook.tail1234.ts.net", tailnetURLFor(tailnetApp, settings, true))
	assert.Equal(t, "https://writebook.tail1234.ts.net", funnelURLFor(tailnetApp, settings, true))

	assert.Empty(t, tailnetURLFor(tailnetApp, settings, false), "disabled -> no URL even for a tailnet app")
	assert.Empty(t, tailnetURLFor(localApp, settings, true), "non-tailnet app -> no URL even when enabled")
	assert.Empty(t, funnelURLFor(localApp, settings, true))
}

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
