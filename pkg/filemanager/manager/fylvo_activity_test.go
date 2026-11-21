package manager

import (
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/stretchr/testify/require"
)

func TestOldActivityIsRemoved(t *testing.T) {
	e := newStaleEnv(t)
	client := inventory.NewActivityClient(e.client)
	now := time.Now()
	for _, age := range []time.Duration{0, 179 * 24 * time.Hour, 181 * 24 * time.Hour, 400 * 24 * time.Hour} {
		require.NoError(t, e.client.Activity.Create().SetType(30).SetCreatedAt(now.Add(-age)).Exec(e.ctx))
	}

	require.Equal(t, 2, removeOldActivity(e.ctx, e.dep, client, now), "older than the default of 180 days")
	left, err := e.client.Activity.Query().Count(e.ctx)
	require.NoError(t, err)
	require.Equal(t, 2, left)

	// 0 keeps everything.
	require.NoError(t, e.dep.SettingClient().Set(e.ctx, map[string]string{inventory.ActivityRetentionSetting: "0"}))
	require.Zero(t, removeOldActivity(e.ctx, e.dep, client, now.Add(1000*24*time.Hour)))
}
