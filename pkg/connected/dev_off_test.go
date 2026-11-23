//go:build !fylvo_dev

package connected

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// An ordinary build has no simulated provider, whatever the environment says.
func TestNoSimulatedProviderInAnOrdinaryBuild(t *testing.T) {
	t.Setenv("FYLVO_DEV_STORAGE", "1")
	require.Nil(t, DevHandler())

	options := ManagerOptions{}
	devOptions(&options)
	require.Nil(t, options.Specs)
	require.Nil(t, options.HTTP)
}

// Apps connect storages only where the installation and the call are https, and not at
// all where the operator switched it off.
func TestAppSigninNeedsHTTPS(t *testing.T) {
	address := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return u
	}
	secure, other, plain := address("https://cloud.example.com"), address("https://files.example.com"), address("http://192.168.1.10:5212")
	require.True(t, AppSignin(secure, secure))
	require.True(t, AppSignin(secure, other))
	require.False(t, AppSignin(secure, plain))
	require.False(t, AppSignin(plain, secure))
	require.False(t, AppSignin(plain, plain))

	t.Setenv(EnvAppSignin, "0")
	require.False(t, AppSignin(secure, secure))
	for _, on := range []string{"", "1", "true", "off"} {
		t.Setenv(EnvAppSignin, on)
		require.True(t, AppSignin(secure, secure), on)
	}
}
