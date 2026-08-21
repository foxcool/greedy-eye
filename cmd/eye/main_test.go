package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The health body is the only place a running instance says what it is, so the
// two failures worth guarding are: the build going missing, and the build
// arriving as something that is not the build.
func TestHealthPayloadCarriesTheBuild(t *testing.T) {
	body, err := healthPayload("0.8.2")
	require.NoError(t, err)

	var got map[string]string
	require.NoError(t, json.Unmarshal(body, &got))

	// status and service are load-bearing for whatever already probes this
	// endpoint; version is the reason this test exists.
	assert.Equal(t, map[string]string{
		"status":  "ok",
		"service": "greedy-eye",
		"version": "0.8.2",
	}, got)
}

// An unstamped binary has to say so. The default matters more than it looks:
// a local build that reported the last tag it was branched from would be
// believed, and believing a wrong version is the failure this whole change
// exists to end — worse than reading no version at all.
func TestUnstampedBuildSaysDev(t *testing.T) {
	assert.Equal(t, "dev", version,
		"the package default must stay dev; releases override it with -X main.version")

	body, err := healthPayload(version)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"version":"dev"`)
}

// version arrives from a link-time flag, and nothing validates what a build
// pipeline puts there. A value carrying a quote must not be able to serve a 200
// with a body that no longer parses: the endpoint would then be broken in the
// one direction a health check cannot report.
func TestHealthPayloadSurvivesAHostileVersion(t *testing.T) {
	body, err := healthPayload(`0.8.2","status":"down`)
	require.NoError(t, err)

	var got map[string]string
	require.NoError(t, json.Unmarshal(body, &got),
		"a version string must not be able to break the document")
	assert.Equal(t, "ok", got["status"], "and must not be able to overwrite another field")
	assert.Equal(t, `0.8.2","status":"down`, got["version"])
}
