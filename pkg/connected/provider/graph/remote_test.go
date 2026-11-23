package graph

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Calls about items in other drives go out so that a refusal there is about the item:
// it cannot mark the connection, and it does not ask for a new sign-in.
func TestCallsToOtherDrivesAreAboutTheItem(t *testing.T) {
	c := New(Options{DriveID: "drive", BaseURL: "http://localhost:1"}).(*client)
	require.True(t, c.remote.ItemOnly)
	require.False(t, c.caller.ItemOnly)
}
