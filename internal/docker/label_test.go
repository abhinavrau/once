package docker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnknownFieldNamedAfterTheCarrySurvives(t *testing.T) {
	// The carry itself is unexported, so it claims no JSON key: a newer version
	// of Once writing a field literally called "extra" (or "labelExtra") must
	// have it preserved like any other unknown field, not shed.
	label := `{"name":"app","host":"app.localhost","extra":{"nested":true},"labelExtra":"on"}`

	settings, err := UnmarshalApplicationSettings(label)
	require.NoError(t, err)

	out := settings.Marshal()
	assert.Contains(t, out, `"extra":{"nested":true}`)
	assert.Contains(t, out, `"labelExtra":"on"`)

	again, err := UnmarshalApplicationSettings(out)
	require.NoError(t, err)
	assert.Contains(t, again.Marshal(), `"extra":{"nested":true}`)
	assert.Contains(t, again.Marshal(), `"labelExtra":"on"`)
}

func TestEncodingWithoutUnknownFieldsMatchesPlainJSON(t *testing.T) {
	// With nothing to merge, the codec must hand back exactly what json.Marshal
	// produces: no detour through a map, which would reorder and reformat the
	// label for every app that has never met a newer version of Once.
	settings := ApplicationSettings{
		Name:      "app",
		Image:     "img:latest",
		Host:      "app.localhost",
		Exposure:  ExposureLocal,
		EnvVars:   map[string]string{"KEY": "value"},
		Resources: ContainerResources{CPUs: 2},
	}

	plain, err := json.Marshal(settings)
	require.NoError(t, err)
	assert.Equal(t, string(plain), settings.Marshal())

	// And still after a decode that found no unknown fields.
	decoded, err := UnmarshalApplicationSettings(string(plain))
	require.NoError(t, err)
	assert.Equal(t, string(plain), decoded.Marshal())
}

func TestRemovedFieldsAreShedNotPreserved(t *testing.T) {
	// A field deliberately deleted from the schema is not an unknown field from
	// a newer version: it goes, and the unknown fields beside it stay.
	label := `{"name":"app","host":"app.localhost","tailscaleExcluded":true,"futureFlag":"on"}`

	settings, err := UnmarshalApplicationSettings(label)
	require.NoError(t, err)

	out := settings.Marshal()
	assert.NotContains(t, out, "tailscaleExcluded")
	assert.Contains(t, out, `"futureFlag":"on"`)

	again, err := UnmarshalApplicationSettings(out)
	require.NoError(t, err)
	assert.NotContains(t, again.Marshal(), "tailscaleExcluded")
}

func TestModelledFieldsWinOverPreservedLabelFields(t *testing.T) {
	// Merging preserved fields back in must never resurrect a value this binary
	// models and has since changed, nor write a modelled key twice.
	label := `{"name":"app","host":"app.localhost","exposure":"local","futureFlag":"on"}`

	settings, err := UnmarshalApplicationSettings(label)
	require.NoError(t, err)

	settings.Host = "app.example.com"
	settings.Exposure = ExposurePublic

	out := settings.Marshal()
	assert.Contains(t, out, `"host":"app.example.com"`)
	assert.Contains(t, out, `"exposure":"public"`)
	assert.NotContains(t, out, "app.localhost")
	assert.Equal(t, 1, strings.Count(out, `"host"`))
	assert.Equal(t, 1, strings.Count(out, `"exposure"`))
	assert.Contains(t, out, `"futureFlag":"on"`)
}
