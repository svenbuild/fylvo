package setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddSiteURLFromEnv(t *testing.T) {
	t.Setenv(EnvSiteURL, "")
	settings := map[string]any{}
	require.False(t, addSiteURLFromEnv(settings))
	require.Empty(t, settings)

	t.Setenv(EnvSiteURL, " https://cloud.example.com/ ")
	require.True(t, addSiteURLFromEnv(settings))
	require.Equal(t, "https://cloud.example.com", settings["siteURL"])

	// CR_SETTING_siteURL wins.
	settings = map[string]any{"siteURL": "https://other.example.com"}
	require.False(t, addSiteURLFromEnv(settings))
	require.Equal(t, "https://other.example.com", settings["siteURL"])
}
