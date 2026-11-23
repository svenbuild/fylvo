//go:build fylvo_dev

package connected

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// The simulated provider starts once, with the first call that asks for it.
	os.Setenv(DevEnv, "1")
	os.Exit(m.Run())
}

// The simulated provider of a development build, through the same steps as a real one.
func TestDevProvider(t *testing.T) {
	require.NotNil(t, DevHandler())
	ctx := context.Background()
	options := ManagerOptions{
		Storages: inventory.NewConnectedStorageClient(newManagerFixture(t).db),
		Settings: &memSettings{values: map[string]string{}},
		Sealer:   testSealer(1),
	}
	stored := options.Settings.(*memSettings)
	devOptions(&options)
	m := NewManager(options)
	// The simulated provider shows as set up, and nothing of it is stored: a server
	// started without it on the same data does not know it.
	setup, err := m.Setup(ctx)
	require.NoError(t, err)
	require.Equal(t, "simulated", setup[ProviderDrive].ClientID)
	secret := "another"
	require.NoError(t, m.SetProvider(ctx, ProviderDrive, "another", &secret))
	require.Empty(t, stored.values[clientIDSetting(ProviderDrive)])
	require.Empty(t, stored.values[clientSecretSetting(ProviderDrive)])

	page := func(method, address string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		DevHandler().ServeHTTP(rec, httptest.NewRequest(method, address, nil))
		return rec
	}

	// The sign-in page is served by the installation itself, under its own address.
	address, cookie, err := m.Authorize(ctx, 7, ProviderDrive, "", testRedirect)
	require.NoError(t, err)
	consent, err := url.Parse(address)
	require.NoError(t, err)
	require.Equal(t, "cloud.example.com", consent.Host)
	require.Equal(t, DevPath+"authorize", consent.Path)
	rec := page(http.MethodGet, address)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Allow as alex@example.com")
	require.Contains(t, rec.Body.String(), "error=access_denied")
	// It sends the browser back to this installation only.
	elsewhere := consent.Query()
	elsewhere.Set("redirect_uri", "https://other.example.com/callback")
	require.Equal(t, http.StatusBadRequest, page(http.MethodGet, "https://cloud.example.com"+DevPath+"authorize?"+elsewhere.Encode()).Code)

	finish, err := m.Callback(ctx, consent.Query().Get("state"), cookie, "alex", "")
	require.NoError(t, err)
	storage, err := m.Finish(ctx, 7, finish)
	require.NoError(t, err)
	require.Equal(t, "alex@example.com", storage.Account)

	list := func(path string) (*Page, error) {
		conn, opCtx, done, err := m.Use(ctx, 7, storage.ID)
		if err != nil {
			return nil, err
		}
		defer done()
		return m.Tree().List(opCtx, conn, path, Order{}, 100, "")
	}
	root, err := list("")
	require.NoError(t, err)
	require.Contains(t, rowNames(root.Rows), "Documents|Empty folder|Many files|Pictures")
	require.Equal(t, 1, root.Omitted)

	// The switch ends all access: the next call finds the token refused, and so the
	// renewal of it.
	require.Equal(t, http.StatusOK, page(http.MethodPost, "https://cloud.example.com"+DevPath+"expire").Code)
	_, err = list("Documents")
	require.Equal(t, provider.KindReconnect, provider.KindOf(err))
	_, _, _, err = m.Use(ctx, 7, storage.ID)
	require.ErrorIs(t, err, ErrReconnect)

	// Signing in again as the same account brings it back.
	address, cookie, err = m.Authorize(ctx, 7, ProviderDrive, storage.ID, testRedirect)
	require.NoError(t, err)
	consent, _ = url.Parse(address)
	finish, err = m.Callback(ctx, consent.Query().Get("state"), cookie, "alex", "")
	require.NoError(t, err)
	_, err = m.Finish(ctx, 7, finish)
	require.NoError(t, err)
	docs, err := list("Documents")
	require.NoError(t, err)
	require.Contains(t, rowNames(docs.Rows), "Budget.gsheet")
}
