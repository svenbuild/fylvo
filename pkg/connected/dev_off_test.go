//go:build !fylvo_dev

package connected

import (
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
