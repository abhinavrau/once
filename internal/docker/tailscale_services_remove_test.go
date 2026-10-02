package docker

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

const servicesListPath = "/tailnet/-/services"

func TestDeleteTaggedServicesDeletesOnlyOncesServices(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, servicesListPath, http.StatusOK, `{"vipServices":[
		{"name":"svc:campfire","tags":["tag:once"]},
		{"name":"svc:once-admin","tags":["tag:once","tag:extra"]},
		{"name":"svc:grafana","tags":["tag:personal"]}
	]}`)
	api.reply(http.MethodDelete, servicesListPath+"/svc:campfire", http.StatusOK, "")
	api.reply(http.MethodDelete, servicesListPath+"/svc:once-admin", http.StatusOK, "")

	deleteTaggedServices(context.Background(), api, "tag:once")

	assert.True(t, api.requested(http.MethodDelete, servicesListPath+"/svc:campfire"))
	assert.True(t, api.requested(http.MethodDelete, servicesListPath+"/svc:once-admin"))
	assert.False(t, api.requested(http.MethodDelete, servicesListPath+"/svc:grafana"), "a Service without Once's tag must never be deleted")
}

func TestDeleteTaggedServicesCarriesOnPastAFailedDelete(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, servicesListPath, http.StatusOK, `{"vipServices":[
		{"name":"svc:campfire","tags":["tag:once"]},
		{"name":"svc:fizzy","tags":["tag:once"]}
	]}`)
	api.fail(http.MethodDelete, servicesListPath+"/svc:campfire", assert.AnError)
	api.reply(http.MethodDelete, servicesListPath+"/svc:fizzy", http.StatusOK, "")

	deleteTaggedServices(context.Background(), api, "tag:once")

	assert.True(t, api.requested(http.MethodDelete, servicesListPath+"/svc:fizzy"))
}

func TestDeleteTaggedServicesIsBestEffortWithoutTheServicesScope(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, servicesListPath, http.StatusForbidden, `{"message":"missing scope"}`)

	deleteTaggedServices(context.Background(), api, "tag:once")

	assert.Empty(t, api.paths(http.MethodDelete))
}

func TestSweepAPISkipsOnControlSeam(t *testing.T) {
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", "tskey-abc")
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", "http://headscale:8080")

	_, ok := sweepAPI(TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once"})

	assert.False(t, ok)
}

func TestSweepAPISkipsWithoutCredentialsOrTag(t *testing.T) {
	_, ok := sweepAPI(TailscaleSettings{Tag: "tag:once"})
	assert.False(t, ok)

	_, ok = sweepAPI(TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
	assert.False(t, ok)
}

func TestSweepAPIUsesTheRuntimeCredential(t *testing.T) {
	_, ok := sweepAPI(TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once"})

	assert.True(t, ok)
}
