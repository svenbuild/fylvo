package middleware

import (
	"context"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// A wrong WebDAV password of a known account is logged, once for a while: clients retry an old
// password all the time. An unknown name is not logged.
func TestWebDAVFailuresAreLoggedOnce(t *testing.T) {
	e := newHoldEnv(t, false)
	r := activity.Start(inventory.NewActivityClient(e.client), logging.NewConsoleLogger(logging.LevelError))
	t.Cleanup(r.Close)

	for _, try := range []struct{ name, password string }{
		{"neu@fylvo.local", "falsch"},
		{"neu@fylvo.local", "falsch"},
		{"neu@fylvo.local", "erstes-passwort"}, // the account password is no WebDAV password
		{"niemand@fylvo.local", "falsch"},
	} {
		c, _ := e.context(stateTokenAuth{valid: true})
		c.Request.SetBasicAuth(try.name, try.password)
		WebDAVAuth()(c)
		require.True(t, c.IsAborted())
	}
	r.Flush()

	entries, err := e.client.Activity.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, int(activity.LoginFailed), entries[0].Type)
	require.Equal(t, e.user.ID, entries[0].OwnerID)
	require.Zero(t, entries[0].UserID)
	require.Equal(t, activity.MethodWebDAV, entries[0].Content.Method)
}
