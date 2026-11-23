package connected

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

type memSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *memSettings) Gets(ctx context.Context, names []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := map[string]string{}
	for _, n := range names {
		res[n] = s.values[n]
	}
	return res, nil
}

func (s *memSettings) Set(ctx context.Context, settings map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range settings {
		s.values[k] = v
	}
	return nil
}

type managerFixture struct {
	*Manager
	db       *ent.Client
	provider *fakeProvider
	settings *memSettings
	closed   []string
	revoked  []string
}

const testRedirect = "https://cloud.example.com/api/v4/fylvo/connected/callback"

func newManagerFixture(t *testing.T) *managerFixture {
	logger := logging.NewConsoleLogger(logging.LevelError)
	db := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = db.Close() })
	db, err := inventory.InitializeDBClient(logger, db, cache.NewMemoStore("", logger), "4.19.1")
	require.NoError(t, err)

	f := &managerFixture{db: db, provider: newFakeProvider(t), settings: &memSettings{values: map[string]string{}}}
	spec := Specs()[ProviderDrive]
	spec.AuthURL = f.provider.URL + "/auth"
	spec.TokenURL = f.provider.URL + "/token"
	spec.Identify = func(ctx context.Context, call APICall) (*Identity, error) {
		return identifyDrive(ctx, call, f.provider.URL)
	}
	spec.Revoke = func(ctx context.Context, client *http.Client, refreshToken string) error {
		f.revoked = append(f.revoked, refreshToken)
		return nil
	}

	f.Manager = NewManager(ManagerOptions{
		Storages: inventory.NewConnectedStorageClient(db),
		Settings: f.settings,
		Sealer:   testSealer(1),
		Specs:    map[string]*Spec{ProviderDrive: spec, ProviderOneDrive: Specs()[ProviderOneDrive]},
		HTTP:     f.provider.Client(),
		Hooks: Hooks{Closed: func(id string, generation int) {
			f.closed = append(f.closed, id)
		}},
	})
	return f
}

// connect runs a whole sign-in for a user and returns the stored connection.
func (f *managerFixture) connect(t *testing.T, userID int, reconnect string) (*Storage, error) {
	t.Helper()
	ctx := context.Background()
	address, cookie, err := f.Authorize(ctx, userID, ProviderDrive, reconnect, testRedirect)
	if err != nil {
		return nil, err
	}
	state, code := f.provider.consent(t, address)
	finish, err := f.Callback(ctx, state, cookie, code, "")
	if err != nil {
		return nil, err
	}
	return f.Finish(ctx, userID, finish)
}

func codeOf(err error) int {
	var app serializer.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	return -1
}

func TestProviderSetup(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()

	providers, err := f.Providers(ctx)
	require.NoError(t, err)
	require.Len(t, providers, 2)
	for _, p := range providers {
		require.False(t, p.Configured, p.Type)
	}
	_, _, err = f.Authorize(ctx, 7, ProviderDrive, "", testRedirect)
	require.Equal(t, serializer.CodeConnectedUnavailable, codeOf(err), "no client set up yet")
	require.Equal(t, ReasonNotConfigured, ReasonOf(err))

	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, " client-id ", &secret))

	// The secret is stored sealed and is shown to nobody.
	stored := f.settings.values[clientSecretSetting(ProviderDrive)]
	require.NotEmpty(t, stored)
	require.NotContains(t, stored, "client-secret")
	setup, err := f.Setup(ctx)
	require.NoError(t, err)
	require.Equal(t, ProviderSetup{ClientID: "client-id", SecretSet: true}, setup[ProviderDrive])
	require.Equal(t, ProviderSetup{}, setup[ProviderOneDrive])
	shown, _ := json.Marshal(setup)
	require.NotContains(t, string(shown), "client-secret")
	require.NotContains(t, string(shown), stored)

	// A new client id without a new secret keeps the secret.
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", nil))
	client, ok, err := f.client(ctx, ProviderDrive)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "client-secret", client.Secret)

	// A secret sealed for one provider does not open as the secret of another.
	f.settings.values[clientIDSetting(ProviderOneDrive)] = "other-id"
	f.settings.values[clientSecretSetting(ProviderOneDrive)] = stored
	_, ok, err = f.client(ctx, ProviderOneDrive)
	require.NoError(t, err)
	require.False(t, ok)

	empty := "  "
	require.Error(t, f.SetProvider(ctx, ProviderDrive, "client-id", &empty))
	require.Error(t, f.SetProvider(ctx, "dropbox", "x", &secret))

	// Removing the client id removes the secret with it.
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "", nil))
	require.Empty(t, f.settings.values[clientSecretSetting(ProviderDrive)])
}

func TestConnectListRenameDisconnect(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", &secret))

	storage, err := f.connect(t, 7, "")
	require.NoError(t, err)
	require.Len(t, storage.ID, 16)
	require.Equal(t, "Google Drive", storage.Name)
	require.Equal(t, "user@example.com", storage.Account)
	require.Equal(t, "cloudreve://"+storage.ID+"@connected", storage.URI)
	require.Equal(t, inventory.ConnectedStatusOK, storage.Status)

	// The token is in the database only in sealed form, and no answer carries it.
	row, err := f.storages.Get(ctx, 7, storage.ID)
	require.NoError(t, err)
	require.False(t, bytes.Contains(row.Secret, []byte("refresh-1")))
	plain, err := f.sealer.Open(ctx, PurposeToken, tokenContext(storage.ID, 7, ProviderDrive), row.Secret)
	require.NoError(t, err)
	token, err := ParseToken(string(plain))
	require.NoError(t, err)
	require.Equal(t, "refresh-1", token.RefreshToken)
	shown, _ := json.Marshal(storage)
	require.NotContains(t, string(shown), "refresh-1")
	require.NotContains(t, string(shown), "access-1")

	// The same provider account cannot be connected twice by one account.
	_, err = f.connect(t, 7, "")
	require.Equal(t, serializer.CodeObjectExist, codeOf(err))
	require.Equal(t, ReasonExists, ReasonOf(err))

	// Each account sees only its own connections.
	list, err := f.List(ctx, 7)
	require.NoError(t, err)
	require.Len(t, list, 1)
	list, err = f.List(ctx, 8)
	require.NoError(t, err)
	require.Empty(t, list)

	// Another account cannot rename or disconnect it, and learns nothing from trying.
	_, err = f.Rename(ctx, 8, storage.ID, "Mine now")
	require.Equal(t, serializer.CodeParentNotExist, codeOf(err))
	require.Equal(t, serializer.CodeParentNotExist, codeOf(f.Disconnect(ctx, 8, storage.ID)))
	require.Equal(t, serializer.CodeParentNotExist, codeOf(f.Disconnect(ctx, 7, "does-not-exist-00")))
	require.Equal(t, serializer.CodeParentNotExist, codeOf(f.Disconnect(ctx, 7, "../../etc/passwd")))
	_, _, err = f.Authorize(ctx, 8, ProviderDrive, storage.ID, testRedirect)
	require.Equal(t, serializer.CodeParentNotExist, codeOf(err), "reconnecting a foreign connection")

	renamed, err := f.Rename(ctx, 7, storage.ID, "  Photos  ")
	require.NoError(t, err)
	require.Equal(t, "Photos", renamed.Name)
	for _, bad := range []string{"", "   ", strings.Repeat("x", 65), "a\nb"} {
		_, err = f.Rename(ctx, 7, storage.ID, bad)
		require.Equal(t, serializer.CodeParamErr, codeOf(err), "%q", bad)
	}

	require.NoError(t, f.Disconnect(ctx, 7, storage.ID))
	list, err = f.List(ctx, 7)
	require.NoError(t, err)
	require.Empty(t, list)
	require.Equal(t, []string{storage.ID}, f.closed)
	require.Equal(t, []string{"refresh-1"}, f.revoked, "the last connection to the account takes the access back")
}

func TestReconnect(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", &secret))

	storage, err := f.connect(t, 7, "")
	require.NoError(t, err)
	require.NoError(t, f.SetProviderForTest(ctx, storage.ID, 7))

	// Signing in again with the same provider account renews the token.
	again, err := f.connect(t, 7, storage.ID)
	require.NoError(t, err)
	require.Equal(t, storage.ID, again.ID)
	require.Equal(t, inventory.ConnectedStatusOK, again.Status)
	row, err := f.storages.Get(ctx, 7, storage.ID)
	require.NoError(t, err)
	require.Equal(t, 2, row.Generation)
	require.Equal(t, []string{storage.ID}, f.closed, "what ran for the old token is ended")
	require.Empty(t, f.revoked)

	// Signing in with another provider account does not take over the connection.
	f.provider.account = "account-2"
	_, err = f.connect(t, 7, storage.ID)
	require.Equal(t, serializer.CodeParamErr, codeOf(err))
	require.Equal(t, ReasonOtherAccount, ReasonOf(err))
	row, err = f.storages.Get(ctx, 7, storage.ID)
	require.NoError(t, err)
	require.Equal(t, 2, row.Generation)

	// Two renewals started at the same time: the second one finds the row changed.
	f.provider.account = "account-1"
	addressA, cookieA, err := f.Authorize(ctx, 7, ProviderDrive, storage.ID, testRedirect)
	require.NoError(t, err)
	addressB, cookieB, err := f.Authorize(ctx, 7, ProviderDrive, storage.ID, testRedirect)
	require.NoError(t, err)
	stateA, codeA := f.provider.consent(t, addressA)
	stateB, codeB := f.provider.consent(t, addressB)
	finishA, err := f.Callback(ctx, stateA, cookieA, codeA, "")
	require.NoError(t, err)
	finishB, err := f.Callback(ctx, stateB, cookieB, codeB, "")
	require.NoError(t, err)
	_, err = f.Finish(ctx, 7, finishA)
	require.NoError(t, err)
	_, err = f.Finish(ctx, 7, finishB)
	require.Error(t, err)
	require.Equal(t, ReasonExpired, ReasonOf(err))
	row, err = f.storages.Get(ctx, 7, storage.ID)
	require.NoError(t, err)
	require.Equal(t, 3, row.Generation)
}

// SetProviderForTest marks a connection as needing a reconnect, as a refused token would.
func (f *managerFixture) SetProviderForTest(ctx context.Context, publicID string, userID int) error {
	row, err := f.storages.Get(ctx, userID, publicID)
	if err != nil {
		return err
	}
	_, err = f.storages.UpdateState(ctx, row.ID, row.Generation, inventory.ConnectedStatusReconnect, "refused", row.Props)
	return err
}

// Taking the access back at the provider would end it for everyone who connected that
// provider account. It happens only for the last connection and never while a sign-in at
// that provider is under way.
func TestRevokeOnlyWhenSafe(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", &secret))

	first, err := f.connect(t, 7, "")
	require.NoError(t, err)
	second, err := f.connect(t, 8, "")
	require.NoError(t, err)

	require.NoError(t, f.Disconnect(ctx, 7, first.ID))
	require.Empty(t, f.revoked, "another account still uses this provider account")

	// A third account is in the middle of signing in.
	address, cookie, err := f.Authorize(ctx, 9, ProviderDrive, "", testRedirect)
	require.NoError(t, err)
	require.NoError(t, f.Disconnect(ctx, 8, second.ID))
	require.Empty(t, f.revoked, "a sign-in at this provider is under way")

	// That sign-in still completes with a working token.
	state, code := f.provider.consent(t, address)
	finish, err := f.Callback(ctx, state, cookie, code, "")
	require.NoError(t, err)
	third, err := f.Finish(ctx, 9, finish)
	require.NoError(t, err)

	require.NoError(t, f.Disconnect(ctx, 9, third.ID))
	require.Len(t, f.revoked, 1)
}

func TestDisconnectUser(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", &secret))

	storage, err := f.connect(t, 7, "")
	require.NoError(t, err)
	other, err := f.connect(t, 8, "")
	require.NoError(t, err)

	require.NoError(t, f.DisconnectUser(ctx, 7))
	list, err := f.List(ctx, 7)
	require.NoError(t, err)
	require.Empty(t, list)
	require.Equal(t, []string{storage.ID}, f.closed)
	list, err = f.List(ctx, 8)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, other.ID, list[0].ID)
}

func TestLimitPerUser(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", &secret))

	for i := 0; i < MaxPerUser; i++ {
		f.provider.account = "account-" + string(rune('a'+i))
		_, err := f.connect(t, 7, "")
		require.NoError(t, err)
	}
	f.provider.account = "one-more"
	_, _, err := f.Authorize(ctx, 7, ProviderDrive, "", testRedirect)
	require.Equal(t, serializer.CodeParamErr, codeOf(err))
	require.Equal(t, ReasonTooMany, ReasonOf(err))
}

// While an account is being deleted nothing new is stored for it: a sign-in that completes
// in that moment must not leave a token behind for an account that is gone.
func TestNothingStoredForAnAccountBeingDeleted(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", &secret))

	address, cookie, err := f.Authorize(ctx, 7, ProviderDrive, "", testRedirect)
	require.NoError(t, err)
	state, code := f.provider.consent(t, address)
	finish, err := f.Callback(ctx, state, cookie, code, "")
	require.NoError(t, err)

	gone := f.Leaving(7)
	require.NoError(t, f.DisconnectUser(ctx, 7))
	_, err = f.Finish(ctx, 7, finish)
	require.Error(t, err)
	list, err := f.List(ctx, 7)
	require.NoError(t, err)
	require.Empty(t, list)

	// Another account is not held up.
	_, err = f.connect(t, 8, "")
	require.NoError(t, err)

	// The deletion was called off: the account can connect again.
	gone()
	gone()
	_, err = f.connect(t, 7, "")
	require.NoError(t, err)
}
