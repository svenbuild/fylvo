package fylvo

import (
	"fmt"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/connectedtest"
	"github.com/stretchr/testify/require"
)

const connectedExampleToken = `{"access_token":"access-1","token_type":"Bearer","refresh_token":"refresh-1","expiry":"2099-01-01T00:00:00Z"}`

// The answers of the routes for connecting storages, one of each kind a client has to
// handle. See connectedtest.Examples for how to get them as files.
func TestConnectedExamples(t *testing.T) {
	e := newConnectedEnv(t, "https://cloud.example.com")
	x := connectedtest.NewExamples(t)
	alex := e.newUser("alex", 2)
	m := connected.For(e.dep)

	// Nothing is set up yet.
	c, _ := e.as(alex, "GET", "/api/v4/fylvo/connected")
	list, err := ConnectedList(c)
	require.NoError(t, err)
	x.Add("storages-not-set-up", list, nil)

	c, _ = e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
	_, err = (&ConnectedAuthorizeService{Provider: connected.ProviderDrive}).Authorize(c)
	require.Equal(t, connected.ReasonNotConfigured, connected.ReasonOf(err))
	x.Add("error-authorize-not-configured", nil, err)

	// The operator enters the client of one provider.
	secret := "the-client-secret"
	c, _ = e.as(alex, "PUT", "/api/v4/admin/fylvo/connected")
	admin, err := (&ConnectedAdminService{Provider: connected.ProviderDrive, ClientID: "client-id.example", ClientSecret: &secret}).Set(c)
	require.NoError(t, err)
	x.Add("admin-setup", admin, nil)

	c, _ = e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
	started, err := (&ConnectedAuthorizeService{Provider: connected.ProviderDrive}).Authorize(c)
	require.NoError(t, err)
	x.Add("authorize", started, nil)

	c, _ = e.as(alex, "POST", "/api/v4/fylvo/connected/finish")
	_, err = (&ConnectedFinishService{Code: "made-up"}).Finish(c)
	require.Equal(t, connected.ReasonExpired, connected.ReasonOf(err))
	x.Add("error-finish-expired", nil, err)

	// Two storages, one of which the provider no longer lets the server into.
	first, err := m.Create(e.ctx, alex.ID, connected.ProviderDrive,
		&connected.Identity{Key: "account-1", Label: "user@example.com"}, connectedExampleToken)
	require.NoError(t, err)
	x.Add("storage", first, nil)
	second, err := m.Create(e.ctx, alex.ID, connected.ProviderDrive,
		&connected.Identity{Key: "account-2", Label: "alex@example.com"}, connectedExampleToken)
	require.NoError(t, err)
	storages := inventory.NewConnectedStorageClient(e.client)
	row, err := storages.Get(e.ctx, alex.ID, second.ID)
	require.NoError(t, err)
	_, err = storages.UpdateStatus(e.ctx, row.ID, row.Generation, inventory.ConnectedStatusReconnect,
		"The provider no longer accepts the stored access.")
	require.NoError(t, err)

	c, _ = e.as(alex, "GET", "/api/v4/fylvo/connected")
	list, err = ConnectedList(c)
	require.NoError(t, err)
	require.Len(t, list.Storages, 2)
	x.Add("storages", list, nil)

	// The same account at the provider a second time.
	_, err = m.Create(e.ctx, alex.ID, connected.ProviderDrive,
		&connected.Identity{Key: "account-1", Label: "user@example.com"}, connectedExampleToken)
	require.Equal(t, connected.ReasonExists, connected.ReasonOf(err))
	x.Add("error-finish-exists", nil, err)

	// As many storages as an account may have.
	for i := 3; i <= connected.MaxPerUser; i++ {
		_, err = m.Create(e.ctx, alex.ID, connected.ProviderDrive,
			&connected.Identity{Key: fmt.Sprintf("account-%d", i), Label: "user@example.com"}, connectedExampleToken)
		require.NoError(t, err)
	}
	c, _ = e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
	_, err = (&ConnectedAuthorizeService{Provider: connected.ProviderDrive}).Authorize(c)
	require.Equal(t, connected.ReasonTooMany, connected.ReasonOf(err))
	x.Add("error-too-many", nil, err)

	// The group loses the permission.
	g, err := e.client.Group.Query().Where(group.ID(2)).First(e.ctx)
	require.NoError(t, err)
	permissions := make(boolset.BooleanSet, len(*g.Permissions))
	copy(permissions, *g.Permissions)
	boolset.Set(types.GroupPermissionConnectedStorage, false, &permissions)
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(&permissions).Exec(e.ctx))
	barred := e.newUser("sam", 2)
	c, _ = e.as(barred, "POST", "/api/v4/fylvo/connected/authorize")
	_, err = (&ConnectedAuthorizeService{Provider: connected.ProviderDrive}).Authorize(c)
	require.Equal(t, connected.ReasonNotAllowed, connected.ReasonOf(err))
	x.Add("error-not-allowed", nil, err)
}
