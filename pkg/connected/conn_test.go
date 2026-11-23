package connected

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// withProvider makes the fixture hand out the given provider for every connection and
// keeps what it was built with.
func (f *managerFixture) withProvider(p provider.Provider) *[]provider.Auth {
	var auths []provider.Auth
	f.specs[ProviderDrive].NewProvider = func(_ *http.Client, auth provider.Auth, _ map[string]string) provider.Provider {
		auths = append(auths, auth)
		return p
	}
	return &auths
}

func connectedFixture(t *testing.T) (*managerFixture, *Storage, *[]provider.Auth) {
	f := newManagerFixture(t)
	secret := "client-secret"
	require.NoError(t, f.SetProvider(context.Background(), ProviderDrive, "client-id", &secret))
	auths := f.withProvider(newMem(false))
	storage, err := f.connect(t, 7, "")
	require.NoError(t, err)
	return f, storage, auths
}

// expire makes the open token of a connection look used up.
func (f *managerFixture) expire(id string) {
	live := f.live[id]
	live.mu.Lock()
	live.token.Expiry = time.Now().Add(-time.Minute)
	live.mu.Unlock()
}

func (f *managerFixture) storedToken(t *testing.T, userID int, id string) *Token {
	row, err := f.storages.Get(context.Background(), userID, id)
	require.NoError(t, err)
	plain, err := f.sealer.Open(context.Background(), PurposeToken, tokenContext(id, userID, row.Provider), row.Secret)
	require.NoError(t, err)
	token, err := ParseToken(string(plain))
	require.NoError(t, err)
	return token
}

func TestUseAdmitsOnlyTheOwner(t *testing.T) {
	f, storage, _ := connectedFixture(t)
	ctx := context.Background()

	c, opCtx, done, err := f.Use(ctx, 7, storage.ID)
	require.NoError(t, err)
	require.Equal(t, storage.ID, c.ID)
	require.Equal(t, 7, c.UserID)
	require.NotNil(t, c.P)
	require.NoError(t, opCtx.Err())
	done()

	// Another account, an unknown id and a malformed id all look like a missing path.
	for _, attempt := range []struct {
		user int
		id   string
	}{{8, storage.ID}, {7, "aaaaaaaaaaaaaaaa"}, {7, "x"}, {7, ""}} {
		_, _, _, err = f.Use(ctx, attempt.user, attempt.id)
		require.Equal(t, serializer.CodeParentNotExist, codeOf(err), "%+v", attempt)
	}

	// A connection that has to be connected again answers so.
	require.NoError(t, f.SetProviderForTest(ctx, storage.ID, 7))
	_, _, _, err = f.Use(ctx, 7, storage.ID)
	require.Equal(t, serializer.CodeConnectedReconnect, codeOf(err))
}

func TestTokenIsRenewedAndStored(t *testing.T) {
	f, storage, auths := connectedFixture(t)
	ctx := context.Background()
	_, _, done, err := f.Use(ctx, 7, storage.ID)
	require.NoError(t, err)
	defer done()
	auth := (*auths)[0]

	// A token with time left is used as it is.
	token, err := auth.Token(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "access-1", token)
	require.Zero(t, f.provider.refreshes)

	// One the provider just refused is renewed, once, however many calls report it.
	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = auth.Token(ctx, "access-1")
		}()
	}
	wg.Wait()
	for _, got := range results {
		require.Equal(t, "access-r1", got)
	}
	require.Equal(t, 1, f.provider.refreshes)
	require.Equal(t, []string{"refresh-1"}, f.provider.refreshed)

	// The provider sent no new refresh token: the stored one stays.
	stored := f.storedToken(t, 7, storage.ID)
	require.Equal(t, "access-r1", stored.AccessToken)
	require.Equal(t, "refresh-1", stored.RefreshToken)

	// A provider that rotates: the new refresh token is in the database before it is used.
	f.provider.rotate = true
	f.expire(storage.ID)
	token, err = auth.Token(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "access-r2", token)
	stored = f.storedToken(t, 7, storage.ID)
	require.Equal(t, "refresh-r2", stored.RefreshToken)
	f.expire(storage.ID)
	_, err = auth.Token(ctx, "")
	require.NoError(t, err)
	require.Equal(t, []string{"refresh-1", "refresh-1", "refresh-r2"}, f.provider.refreshed)
}

func TestRefusedRefresh(t *testing.T) {
	f, storage, auths := connectedFixture(t)
	ctx := context.Background()
	_, opCtx, done, err := f.Use(ctx, 7, storage.ID)
	require.NoError(t, err)
	defer done()
	auth := (*auths)[0]

	// The client of the installation is refused: a matter for the operator, the
	// connection is not marked.
	f.provider.refuse = "invalid_client"
	f.expire(storage.ID)
	_, err = auth.Token(ctx, "")
	require.Equal(t, provider.KindSetup, provider.KindOf(err))
	require.NotContains(t, err.Error(), "secret detail")
	row, _ := f.storages.Get(ctx, 7, storage.ID)
	require.Equal(t, inventory.ConnectedStatusOK, row.Status)
	require.NoError(t, opCtx.Err())

	// The provider no longer accepts the access: the connection has to be made again,
	// and what runs for it ends.
	f.provider.refuse = "invalid_grant"
	_, err = auth.Token(ctx, "")
	require.Equal(t, provider.KindReconnect, provider.KindOf(err))
	row, _ = f.storages.Get(ctx, 7, storage.ID)
	require.Equal(t, inventory.ConnectedStatusReconnect, row.Status)
	ends(t, opCtx)
	_, _, _, err = f.Use(ctx, 7, storage.ID)
	require.Equal(t, serializer.CodeConnectedReconnect, codeOf(err))
	require.Equal(t, serializer.CodeConnectedReconnect, codeOf(Error(ctx, &provider.Error{Kind: provider.KindReconnect})))

	// Signing in again brings it back, with a provider client of its own.
	f.provider.refuse = ""
	_, err = f.connect(t, 7, storage.ID)
	require.NoError(t, err)
	_, opCtx2, done2, err := f.Use(ctx, 7, storage.ID)
	require.NoError(t, err)
	defer done2()
	require.NoError(t, opCtx2.Err())
	require.Len(t, *auths, 2)
}

func TestDisconnectStopsWhatRuns(t *testing.T) {
	f, storage, auths := connectedFixture(t)
	ctx := context.Background()
	c, opCtx, done, err := f.Use(ctx, 7, storage.ID)
	require.NoError(t, err)
	defer done()

	// Something is listed, so the tree holds data of the connection.
	_, err = f.Tree().List(opCtx, c, "", Order{}, 10, "")
	require.NoError(t, err)

	require.NoError(t, f.Disconnect(ctx, 7, storage.ID))
	ends(t, opCtx)
	require.Empty(t, f.live)
	require.Empty(t, f.Tree().listings)

	// The token it held in memory is good for nothing any more.
	_, err = (*auths)[0].Token(ctx, "access-1")
	require.Error(t, err)
	require.Zero(t, f.provider.refreshes, "no refresh for a connection that ended")

	_, _, _, err = f.Use(ctx, 7, storage.ID)
	require.Equal(t, serializer.CodeParentNotExist, codeOf(err))
}

// A refresh that ends after the connection got a new token must not write the old one back.
func TestLateRefreshDoesNotOverwriteANewToken(t *testing.T) {
	f, storage, auths := connectedFixture(t)
	ctx := context.Background()
	_, _, done, err := f.Use(ctx, 7, storage.ID)
	require.NoError(t, err)
	defer done()
	old := (*auths)[0].(*liveConn)

	_, err = f.connect(t, 7, storage.ID)
	require.NoError(t, err)
	fresh := f.storedToken(t, 7, storage.ID)

	// The old generation was closed; force its refresh path anyway.
	old.ctx, old.cancel = context.WithCancel(context.Background())
	old.token.Expiry = time.Now().Add(-time.Minute)
	_, err = old.Token(ctx, "")
	require.Error(t, err)
	require.Equal(t, fresh.AccessToken, f.storedToken(t, 7, storage.ID).AccessToken)
	row, _ := f.storages.Get(ctx, 7, storage.ID)
	require.Equal(t, inventory.ConnectedStatusOK, row.Status)
}

func TestErrorMapping(t *testing.T) {
	ctx := context.Background()
	cases := map[provider.Kind]int{
		provider.KindNotFound:  serializer.CodeParentNotExist,
		provider.KindExists:    serializer.CodeObjectExist,
		provider.KindReconnect: serializer.CodeConnectedReconnect,
		provider.KindSetup:     serializer.CodeConnectedUnavailable,
		provider.KindBusy:      serializer.CodeConnectedBusy,
		provider.KindFull:      serializer.CodeConnectedFull,
		provider.KindForbidden: serializer.CodeConnectedItem,
		provider.KindStale:     serializer.CodeEntityNotExist,
	}
	for kind, code := range cases {
		require.Equal(t, code, codeOf(Error(ctx, &provider.Error{Kind: kind, Code: "x"})), "kind %d", kind)
	}
	require.Equal(t, ReasonForbidden, ReasonOf(Error(ctx, &provider.Error{Kind: provider.KindForbidden})))
	require.Equal(t, ReasonAmbiguous, ReasonOf(Error(ctx, ErrAmbiguous)))
	require.Equal(t, ReasonTooLarge, ReasonOf(Error(ctx, ErrTooLarge)))
	require.Equal(t, serializer.CodeParentNotExist, codeOf(Error(ctx, ErrNotFound)))
	require.Equal(t, serializer.CodeConnectedListingGone, codeOf(Error(ctx, ErrListingGone)))
	require.Equal(t, serializer.CodeParamErr, codeOf(Error(ctx, ErrBadPath)))
	require.Equal(t, serializer.CodeConnectedBusy, codeOf(Error(ctx, context.DeadlineExceeded)))
	require.Empty(t, ReasonOf(Error(ctx, ErrNotFound)))
	require.NoError(t, Error(ctx, nil))
	// What has no answer of its own shows nothing of itself to the client.
	unknown := Error(ctx, errors.New("internal detail"))
	require.Equal(t, serializer.CodeInternalSetting, codeOf(unknown))
	require.NotContains(t, unknown.Error(), "internal detail")
	// An error that already is one of the API is passed on as it is.
	require.Equal(t, serializer.CodeConnectedReconnect, codeOf(Error(ctx, ErrReconnect)))
}

// A provider that refuses a token it has just handed out: the storage shows as one that
// has to be connected again, as the calls say.
func TestRefusedFreshTokenMarksReconnect(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", &secret))
	auths := f.withProvider(newMem(false))
	storage, err := f.connect(t, 7, "")
	require.NoError(t, err)
	_, _, done, err := f.Use(ctx, 7, storage.ID)
	require.NoError(t, err)
	defer done()

	auth := (*auths)[0]
	token, err := auth.Token(ctx, "")
	require.NoError(t, err)
	// Some other token: nothing happens.
	auth.(provider.Refusable).Refused(ctx, "another-token")
	row, _ := f.storages.Get(ctx, 7, storage.ID)
	require.Equal(t, inventory.ConnectedStatusOK, row.Status)

	auth.(provider.Refusable).Refused(ctx, token)
	row, _ = f.storages.Get(ctx, 7, storage.ID)
	require.Equal(t, inventory.ConnectedStatusReconnect, row.Status)
	_, _, _, err = f.Use(ctx, 7, storage.ID)
	require.ErrorIs(t, err, ErrReconnect)
}

// ends waits for the context of a request to end: it follows the end of its connection
// a moment later.
func ends(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a request that was running was not stopped")
	}
}
