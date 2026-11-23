package connected

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// tokenMargin is how much of an access token must be left for it to be used.
const tokenMargin = time.Minute

// Conn is a connection in use by one request.
type Conn struct {
	// ID is the public id of the connection.
	ID         string
	UserID     int
	Provider   string
	Generation int
	// AccountKey names the provider account; two connections with the same key work
	// on the same files.
	AccountKey string
	// Name is the name the account gave the connection.
	Name string
	// P talks to the provider for this connection.
	P provider.Provider

	live *liveConn
}

// liveConn is what the server keeps in memory for a connection of one generation while
// it is in use: the provider client, the open token and the means to stop everything.
type liveConn struct {
	id         string
	rowID      int
	userID     int
	kind       string
	generation int

	ctx    context.Context
	cancel context.CancelFunc
	p      provider.Provider

	m *Manager

	// mu guards the token and makes sure one refresh runs at a time.
	mu    sync.Mutex
	token *Token

	// writes lets one change of the storage run at a time (see write.go).
	writes sync.Mutex
}

// Use admits a request to a connection of the account. It returns the connection and a
// context that ends with the request and also the moment the connection is disconnected
// or gets a new token. done must be called when the request is over.
//
// A connection of another account is not found (ErrNoStorage); one that has to be
// connected again answers ErrReconnect.
func (m *Manager) Use(ctx context.Context, userID int, publicID string) (c *Conn, opCtx context.Context, done func(), err error) {
	// Shared with every other request, exclusive against disconnecting and renewing:
	// what is admitted here saw the row as it is, and is stopped when the row ends.
	m.mu.RLock()
	defer m.mu.RUnlock()

	row, err := m.row(ctx, userID, publicID)
	if err != nil {
		return nil, nil, nil, err
	}
	if row.Status == inventory.ConnectedStatusReconnect {
		return nil, nil, nil, ErrReconnect
	}
	spec := m.specs[row.Provider]
	if spec == nil || spec.NewProvider == nil {
		return nil, nil, nil, ErrUnavailable
	}

	m.liveMu.Lock()
	live := m.live[row.PublicID]
	if live == nil || live.generation != row.Generation {
		if live != nil {
			live.cancel()
		}
		plain, err := m.sealer.Open(ctx, PurposeToken, tokenContext(row.PublicID, row.UserID, row.Provider), row.Secret)
		if err != nil {
			m.liveMu.Unlock()
			// Sealed with another master key: the account has to connect again.
			return nil, nil, nil, ErrReconnect
		}
		token, err := ParseToken(string(plain))
		if err != nil {
			m.liveMu.Unlock()
			return nil, nil, nil, ErrReconnect
		}

		live = &liveConn{id: row.PublicID, rowID: row.ID, userID: row.UserID, kind: row.Provider,
			generation: row.Generation, m: m, token: token}
		live.ctx, live.cancel = context.WithCancel(context.Background())
		props := map[string]string{}
		if row.Props != nil {
			props["drive_id"] = row.Props.DriveID
			props["drive_type"] = row.Props.DriveType
		}
		live.p = spec.NewProvider(m.http, live, props)
		m.live[row.PublicID] = live
	}
	m.liveMu.Unlock()

	opCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(live.ctx, cancel)
	return &Conn{
			ID: row.PublicID, UserID: row.UserID, Provider: row.Provider, Generation: row.Generation,
			AccountKey: row.Provider + "|" + row.AccountKey, Name: row.Name, P: live.p, live: live,
		}, opCtx, func() {
			stop()
			cancel()
		}, nil
}

// ended tells whether the connection this request was admitted to has ended since. The
// context of the request follows a moment later; this is true at once.
func (c *Conn) ended() bool {
	return c.live != nil && c.live.ctx.Err() != nil
}

// closeLive stops everything running for a connection and forgets its open token. The
// caller holds m.mu exclusively.
func (m *Manager) closeLive(publicID string) {
	m.liveMu.Lock()
	live := m.live[publicID]
	delete(m.live, publicID)
	for key := range m.usage {
		if strings.HasPrefix(key, publicID+"#") {
			delete(m.usage, key)
		}
	}
	m.liveMu.Unlock()
	if live != nil {
		live.cancel()
	}
	m.tree.Forget(publicID)
	m.uploads.close(publicID)
}

// Refused implements provider.Refusable: the provider refused a token it had handed out
// a moment before, so the connection has to be connected again, like after a refused
// renewal.
func (l *liveConn) Refused(ctx context.Context, token string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.token != nil && l.token.AccessToken == token {
		l.markReconnect(context.WithoutCancel(ctx))
	}
}

// Fresh reads the state of the content a row shows right now into the row: its own,
// or for a link that of the item it points to. A listing can be some seconds old, and
// of what a link points to it tells nothing.
func Fresh(ctx context.Context, p provider.Provider, row *Row) error {
	var (
		current *provider.Item
		err     error
	)
	if row.Link != nil {
		current, err = p.Target(ctx, &row.Item)
	} else {
		current, err = p.Get(ctx, row.ID)
	}
	if err != nil {
		return err
	}
	row.Size, row.ModTime, row.Hash = current.Size, current.ModTime, current.Hash
	row.Version, row.Revision = current.Version, current.Revision
	return nil
}

// Public returns err as it may show in a message to the client: an error of the API with
// its code and text, without the cause, which goes to the log. The message of an answer
// that sums up several errors is made of their texts.
func Public(ctx context.Context, err error) error {
	err = Error(ctx, err)
	var app serializer.AppError
	if !errors.As(err, &app) || app.RawError == nil {
		return err
	}
	if _, isReason := app.RawError.(reasonError); isReason {
		return err
	}
	logging.FromContext(ctx).Warning("Connected storage: %s", err)
	return serializer.NewError(app.ErrCode(), app.Msg, nil)
}

// Token implements provider.Auth: it returns the access token of the connection and
// renews it first when it runs out or was just refused.
func (l *liveConn) Token(ctx context.Context, rejected string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.ctx.Err() != nil {
		return "", &provider.Error{Kind: provider.KindReconnect}
	}
	current := l.token
	if current.AccessToken != "" && current.AccessToken != rejected && time.Until(current.Expiry) > tokenMargin {
		return current.AccessToken, nil
	}

	renewed, err := l.refresh(ctx, current)
	if err != nil {
		return "", err
	}
	l.token = renewed
	return renewed.AccessToken, nil
}

// refresh gets a new access token from the provider and stores it. The caller holds l.mu.
func (l *liveConn) refresh(ctx context.Context, current *Token) (*Token, error) {
	m := l.m
	spec := m.specs[l.kind]
	client, ok, err := m.client(ctx, l.kind)
	if err != nil {
		return nil, &provider.Error{Kind: provider.KindBusy}
	}
	if !ok {
		return nil, &provider.Error{Kind: provider.KindSetup}
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {current.RefreshToken},
		"client_id":     {client.ID},
		"client_secret": {client.Secret},
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, spec.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &provider.Error{Kind: provider.KindOther}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := m.flow.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &provider.Error{Kind: provider.KindBusy}
	}
	defer res.Body.Close()

	var answer struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	_ = json.Unmarshal(raw, &answer)

	switch {
	case answer.Error == "invalid_grant":
		// The provider no longer accepts the stored access: revoked, expired or unused
		// for too long. Only signing in again helps.
		l.markReconnect(context.WithoutCancel(ctx))
		return nil, &provider.Error{Kind: provider.KindReconnect, Status: res.StatusCode, Code: answer.Error}
	case answer.Error == "invalid_client" || answer.Error == "unauthorized_client":
		return nil, &provider.Error{Kind: provider.KindSetup, Status: res.StatusCode, Code: answer.Error}
	case res.StatusCode != http.StatusOK || answer.AccessToken == "":
		return nil, &provider.Error{Kind: provider.KindBusy, Status: res.StatusCode, Code: answer.Error}
	}

	renewed := &Token{
		AccessToken:  answer.AccessToken,
		TokenType:    answer.TokenType,
		RefreshToken: answer.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(answer.ExpiresIn) * time.Second).UTC(),
	}
	if renewed.TokenType == "" {
		renewed.TokenType = "Bearer"
	}
	if renewed.RefreshToken == "" {
		// Google sends none on a refresh: the stored one stays.
		renewed.RefreshToken = current.RefreshToken
	}

	// Stored before anyone uses it: a provider that rotates refresh tokens must not be
	// left with one only this process knows. So also when the request that asked for
	// the token has gone away meanwhile.
	storeCtx, cancelStore := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelStore()
	sealed, err := m.sealer.Seal(storeCtx, PurposeToken, tokenContext(l.id, l.userID, l.kind), []byte(renewed.String()))
	if err != nil {
		return nil, &provider.Error{Kind: provider.KindOther}
	}
	stored, err := m.storages.UpdateSecret(storeCtx, l.rowID, l.generation, sealed)
	if err != nil {
		return nil, &provider.Error{Kind: provider.KindBusy}
	}
	if !stored {
		// The connection ended or was renewed meanwhile. This token belongs to nobody.
		l.cancel()
		return nil, &provider.Error{Kind: provider.KindReconnect}
	}
	return renewed, nil
}

// markReconnect notes that the connection has to be connected again, for the generation
// this token belongs to. A connection renewed meanwhile is left alone.
func (l *liveConn) markReconnect(ctx context.Context) {
	// Callers hold the lock of the token: a database that stalls must not hold up
	// every call of the connection for good.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	marked, err := l.m.storages.UpdateStatus(ctx, l.rowID, l.generation, inventory.ConnectedStatusReconnect,
		"The provider no longer accepts the stored access.")
	if err != nil || !marked {
		return
	}
	l.cancel()
}

// Error turns an error of a provider into the error the API reports. The provider's own
// text is never part of it; what has no answer of its own goes to the log.
func Error(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var app serializer.AppError
	if errors.As(err, &app) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrBusy
	}
	if errors.Is(err, ErrAmbiguous) {
		return ItemError(ReasonAmbiguous)
	}
	if errors.Is(err, ErrNotFound) {
		return ErrNoStorage
	}
	if errors.Is(err, ErrListingGone) {
		return serializer.NewError(serializer.CodeConnectedListingGone, "The listing expired, start again", nil)
	}
	if errors.Is(err, ErrTooLarge) {
		return ItemError(ReasonTooLarge)
	}
	if errors.Is(err, ErrBadPath) {
		return serializer.NewError(serializer.CodeParamErr, "Invalid path", nil)
	}
	switch provider.KindOf(err) {
	case provider.KindNotFound:
		return ErrNoStorage
	case provider.KindExists:
		return serializer.NewError(serializer.CodeObjectExist, "Object existed", nil)
	case provider.KindReconnect:
		return ErrReconnect
	case provider.KindSetup:
		return ErrUnavailable
	case provider.KindBusy, provider.KindIncomplete:
		return ErrBusy
	case provider.KindFull:
		return ErrFull
	case provider.KindForbidden:
		return ItemError(ReasonForbidden)
	case provider.KindFlagged:
		return ItemError(ReasonFlagged)
	case provider.KindExportTooLarge:
		return ItemError(ReasonExportTooLarge)
	case provider.KindDailyLimit:
		return ItemError(ReasonDailyLimit)
	case provider.KindStale:
		return serializer.NewError(serializer.CodeEntityNotExist, "This version is no longer available", nil)
	}
	logging.FromContext(ctx).Warning("Connected storage: %s", err)
	return serializer.NewError(serializer.CodeInternalSetting, "The storage provider answered with an error", nil)
}

// Reasons connecting a storage did not work, sent as data.reason.
const (
	// ReasonExpired: the sign-in was not completed in time or was already used.
	ReasonExpired = "expired"
	// ReasonAccount: another Fylvo account started the sign-in.
	ReasonAccount = "account"
	// ReasonOtherAccount: the storage was to be connected again, but with another
	// account at the provider than the one it belongs to.
	ReasonOtherAccount = "other_account"
	// ReasonExists: this account at the provider is already connected.
	ReasonExists = "exists"
	// ReasonTooMany: the account has as many connected storages as it may have.
	ReasonTooMany = "too_many"
	// ReasonSetup: the provider refuses the client of the installation.
	ReasonSetup = "setup"
	// ReasonNotConfigured: the operator has not set the provider up.
	ReasonNotConfigured = "not_configured"
	// ReasonNotAllowed: the group of the account may not connect storages.
	ReasonNotAllowed = "not_allowed"
	// ReasonOtherAddress: the sign-in was started under another address of the
	// installation than its first one.
	ReasonOtherAddress = "other_address"
)

// ErrOtherAddress is the answer to a sign-in started under another address of the
// installation than its first one, which is the one the provider sends the browser back to.
var ErrOtherAddress = reasoned(serializer.CodeParamErr, "Open Fylvo under its main address to connect a storage", ReasonOtherAddress)

// ErrNotAllowed is the answer for an account whose group may not connect storages.
var ErrNotAllowed = reasoned(serializer.CodeConnectedUnavailable, "Connected storages are not available", ReasonNotAllowed)

// Reasons an item cannot be used, sent with code 40925.
const (
	ReasonAmbiguous      = "ambiguous"
	ReasonChanged        = "changed"
	ReasonForbidden      = "forbidden"
	ReasonFlagged        = "flagged"
	ReasonExportTooLarge = "export_too_large"
	ReasonDailyLimit     = "daily_limit"
	ReasonTooLarge       = "too_large"
)

// ReasonData is the data of an error answer that says why as one word for programs.
type ReasonData struct {
	Reason string `json:"reason"`
}

// reasonError carries the reason inside the error of an item that cannot be used.
type reasonError string

func (r reasonError) Error() string { return string(r) }

// ResponseData is what the answer of the API carries as data for such an error.
func (r reasonError) ResponseData() any { return ReasonData{Reason: string(r)} }

// ItemError returns the error for an item that cannot be used, for the given reason.
func ItemError(reason string) error {
	return reasoned(serializer.CodeConnectedItem, "This item cannot be used", reason)
}

// reasoned returns an error of the API that also says, as one word for programs, why.
// The routes of connected storages send that word as data.reason.
func reasoned(code int, message, reason string) error {
	return serializer.NewError(code, message, reasonError(reason))
}

// ReasonOf returns the reason an error of connected storages carries, or "".
func ReasonOf(err error) string {
	var app serializer.AppError
	if errors.As(err, &app) {
		if reason, ok := app.RawError.(reasonError); ok {
			return string(reason)
		}
	}
	return ""
}
