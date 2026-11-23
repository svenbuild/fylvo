package connected

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

const (
	// MaxPerUser is the most storages one account can connect.
	MaxPerUser = 10
	maxNameLen = 64
)

// Errors of connected storages as the API reports them.
var (
	ErrUnsupported = serializer.NewError(serializer.CodeConnectedUnsupported, "Not supported in a connected storage", nil)
	ErrReconnect   = serializer.NewError(serializer.CodeConnectedReconnect, "The storage has to be connected again", nil)
	ErrUnavailable = serializer.NewError(serializer.CodeConnectedUnavailable, "Connected storages are not available", nil)
	ErrBusy        = serializer.NewError(serializer.CodeConnectedBusy, "The storage provider is busy, try again later", nil)
	ErrFull        = serializer.NewError(serializer.CodeConnectedFull, "The storage at the provider is full", nil)
	// ErrNoStorage is the answer for a connection that does not exist or belongs to
	// somebody else: the same as for a path that does not exist.
	ErrNoStorage = serializer.NewError(serializer.CodeParentNotExist, "Path not exist", nil)
)

// SettingStore reads and writes settings of the installation.
type SettingStore interface {
	Gets(ctx context.Context, names []string) (map[string]string, error)
	Set(ctx context.Context, settings map[string]string) error
}

// Storage is a connection as the API shows it. It never carries a token.
type Storage struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Name      string    `json:"name"`
	Account   string    `json:"account"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	URI       string    `json:"uri"`
	CreatedAt time.Time `json:"created_at"`
}

// ProviderInfo tells whether a provider can be used on this installation.
type ProviderInfo struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
}

// ProviderSetup is what the operator sees of a provider's OAuth client.
type ProviderSetup struct {
	ClientID  string `json:"client_id"`
	SecretSet bool   `json:"secret_set"`
}

// Hooks let the part that talks to the providers react to the life of a connection.
type Hooks struct {
	// Closed is called after a connection ended (disconnect, removed account) or got a
	// new token: everything running for it or kept about it has to go. generation is the
	// one that ended.
	Closed func(publicID string, generation int)
}

// ManagerOptions are what a Manager works with.
type ManagerOptions struct {
	Storages inventory.ConnectedStorageClient
	Settings SettingStore
	Sealer   *Sealer
	// Specs are the providers; nil takes Specs().
	Specs map[string]*Spec
	// HTTP talks to the providers; nil takes provider.NewHTTPClient().
	HTTP  *http.Client
	Hooks Hooks
	// UserExists tells whether an account (still) exists. nil: every account does.
	UserExists func(ctx context.Context, userID int) bool
	// ThumbDir is where previews fetched from providers are kept. "" keeps none.
	ThumbDir string
}

// Manager keeps the connections of all accounts: who connected what, the sealed tokens
// and the OAuth clients of the installation.
type Manager struct {
	storages inventory.ConnectedStorageClient
	settings SettingStore
	sealer   *Sealer
	specs    map[string]*Spec
	http     *http.Client
	flow     *Flow
	hooks    Hooks
	exists   func(ctx context.Context, userID int) bool

	// mu orders the steps that decide over tokens of a provider account: storing the
	// result of a sign-in, disconnecting and revoking hold it exclusively. Requests that
	// use a connection hold it shared while they are admitted (see Use).
	mu sync.RWMutex
	// live holds, per connection, what is open in memory while it is in use.
	liveMu sync.Mutex
	live   map[string]*liveConn
	// tree reads the folders of all connections.
	tree *Tree
	// uploads are the upload sessions of all connections (see upload.go).
	uploads *uploads
	// offices are the sessions of files open in the Office server (see office.go).
	offices *offices
	// usage remembers the fill level of connections for a short while (under liveMu).
	usage map[string]usage
	// leaving counts, per account, the deletions of that account that are under way.
	leaving map[int]int
	// starting lets one ticket at a time become a sign-in (see StartTicket).
	starting sync.Mutex
	// writes holds one lock per account at a provider (Conn.AccountKey), see write.go.
	writes sync.Map
	// unsettled are the names a change with an open outcome was about (see write.go).
	unsettled openNames
	// thumbs are the previews fetched from providers (see thumbs.go).
	thumbs *thumbs
}

func NewManager(o ManagerOptions) *Manager {
	if o.Specs == nil {
		o.Specs = Specs()
	}
	if o.HTTP == nil {
		o.HTTP = provider.NewHTTPClient()
	}
	m := &Manager{
		storages: o.Storages,
		settings: o.Settings,
		sealer:   o.Sealer,
		specs:    o.Specs,
		http:     o.HTTP,
		flow:     NewFlow(o.Specs, o.HTTP),
		hooks:    o.Hooks,
		exists:   o.UserExists,
		leaving:  map[int]int{},
		live:     map[string]*liveConn{},
		tree:     NewTree(),
		uploads:  newUploads(),
		offices:  newOffices(),
		usage:    map[string]usage{},
		thumbs:   newThumbs(o.ThumbDir),
	}
	if o.Storages != nil {
		m.thumbs.live = func() ([]string, error) { return o.Storages.PublicIDs(context.Background()) }
	}
	return m
}

// Tree returns what reads the folders of the connections.
func (m *Manager) Tree() *Tree { return m.tree }

// Leaving marks an account as being deleted until the returned function is called. In
// between nothing new is stored for it: a sign-in that completes meanwhile is refused, so
// no token can outlive the account. The caller removes the account's connections with
// DisconnectUser and calls the function when the account is gone, or was kept after all.
func (m *Manager) Leaving(userID int) (done func()) {
	m.mu.Lock()
	m.leaving[userID]++
	m.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if m.leaving[userID]--; m.leaving[userID] <= 0 {
				delete(m.leaving, userID)
			}
			m.mu.Unlock()
		})
	}
}

func clientIDSetting(provider string) string {
	return inventory.ConnectedClientIDSetting(provider)
}

func clientSecretSetting(provider string) string {
	return inventory.ConnectedClientSecretSetting(provider)
}

func (m *Manager) providerTypes() []string {
	kinds := make([]string, 0, len(m.specs))
	for kind := range m.specs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// client returns the OAuth client of the installation for a provider, without the
// redirect address. ok is false when the operator has not set it up.
func (m *Manager) client(ctx context.Context, provider string) (c Client, ok bool, err error) {
	values, err := m.settings.Gets(ctx, []string{clientIDSetting(provider), clientSecretSetting(provider)})
	if err != nil {
		return c, false, serializer.NewError(serializer.CodeDBError, "Failed to read settings", err)
	}
	c.ID = values[clientIDSetting(provider)]
	sealed := values[clientSecretSetting(provider)]
	if c.ID == "" || sealed == "" {
		return c, false, nil
	}

	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return c, false, nil
	}
	secret, err := m.sealer.Open(ctx, PurposeClientSecret, Context(provider), raw)
	if err != nil {
		// Sealed with another master key: the operator has to enter it again.
		return c, false, nil
	}
	c.Secret = string(secret)
	return c, true, nil
}

// Providers lists the providers and whether each one is set up.
func (m *Manager) Providers(ctx context.Context) ([]ProviderInfo, error) {
	res := make([]ProviderInfo, 0, len(m.specs))
	for _, kind := range m.providerTypes() {
		_, ok, err := m.client(ctx, kind)
		if err != nil {
			return nil, err
		}
		res = append(res, ProviderInfo{Type: kind, Name: m.specs[kind].Name, Configured: ok})
	}
	return res, nil
}

// Setup returns what the operator may see of the OAuth clients: never a secret.
func (m *Manager) Setup(ctx context.Context) (map[string]ProviderSetup, error) {
	res := make(map[string]ProviderSetup, len(m.specs))
	for _, kind := range m.providerTypes() {
		values, err := m.settings.Gets(ctx, []string{clientIDSetting(kind), clientSecretSetting(kind)})
		if err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to read settings", err)
		}
		_, usable, err := m.client(ctx, kind)
		if err != nil {
			return nil, err
		}
		res[kind] = ProviderSetup{ClientID: values[clientIDSetting(kind)], SecretSet: usable}
	}
	return res, nil
}

// SetProvider stores the OAuth client of a provider. A nil secret keeps the stored one;
// an empty client id removes the client.
func (m *Manager) SetProvider(ctx context.Context, provider, clientID string, secret *string) error {
	if m.specs[provider] == nil {
		return serializer.NewError(serializer.CodeParamErr, "Unknown provider", nil)
	}
	clientID = strings.TrimSpace(clientID)
	if len(clientID) > 512 || (secret != nil && len(*secret) > 2048) {
		return serializer.NewError(serializer.CodeParamErr, "Value is too long", nil)
	}

	values := map[string]string{clientIDSetting(provider): clientID}
	switch {
	case clientID == "":
		values[clientSecretSetting(provider)] = ""
	case secret != nil:
		value := strings.TrimSpace(*secret)
		if value == "" {
			return serializer.NewError(serializer.CodeParamErr, "The client secret is empty", nil)
		}
		sealed, err := m.sealer.Seal(ctx, PurposeClientSecret, Context(provider), []byte(value))
		if err != nil {
			return serializer.NewError(serializer.CodeInternalSetting, "Failed to seal the client secret", err)
		}
		values[clientSecretSetting(provider)] = base64.StdEncoding.EncodeToString(sealed)
	}

	if err := m.settings.Set(ctx, values); err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to save settings", err)
	}
	return nil
}

func storageOf(row *ent.ConnectedStorage) *Storage {
	return &Storage{
		ID:        row.PublicID,
		Provider:  row.Provider,
		Name:      row.Name,
		Account:   row.Account,
		Status:    row.Status,
		Message:   row.StatusMessage,
		URI:       RootURI(row.PublicID),
		CreatedAt: row.CreatedAt,
	}
}

// RootURI is the address of the root of a connection.
func RootURI(publicID string) string {
	return fmt.Sprintf("%s://%s@%s", constants.CloudreveScheme, publicID, constants.FileSystemConnected)
}

// List returns the connections of an account, oldest first.
func (m *Manager) List(ctx context.Context, userID int) ([]*Storage, error) {
	rows, err := m.storages.ListByUser(ctx, userID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list connected storages", err)
	}
	res := make([]*Storage, 0, len(rows))
	for _, row := range rows {
		res = append(res, storageOf(row))
	}
	return res, nil
}

// Has tells whether the account has a connection with this id.
func (m *Manager) Has(ctx context.Context, userID int, publicID string) bool {
	_, err := m.row(ctx, userID, publicID)
	return err == nil
}

// row returns the connection of an account, ErrNoStorage when it has none with this id.
func (m *Manager) row(ctx context.Context, userID int, publicID string) (*ent.ConnectedStorage, error) {
	if !validPublicID(publicID) {
		return nil, ErrNoStorage
	}
	row, err := m.storages.Get(ctx, userID, publicID)
	if ent.IsNotFound(err) {
		return nil, ErrNoStorage
	}
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to load the connected storage", err)
	}
	return row, nil
}

const publicIDChars = "abcdefghijklmnopqrstuvwxyz0123456789"

func newPublicID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	for i, b := range raw {
		// 256 is not a multiple of 36; the small skew does not matter for an id that
		// is no secret.
		raw[i] = publicIDChars[int(b)%len(publicIDChars)]
	}
	return string(raw)
}

func validPublicID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, c := range id {
		if !strings.ContainsRune(publicIDChars, c) {
			return false
		}
	}
	return true
}

// admit runs the checks every sign-in passes before it starts: the provider is one of
// this version and set up, the account is there and not being removed, and either the
// connection to renew is one of it at this provider or it has room for one more. It
// returns the client of the installation (without the redirect address) and the
// connection to renew. The caller holds m.mu, so nothing starts for an account whose
// removal has begun: its sign-ins were dropped, and one that started now would leave a
// token behind when the provider answers. loaded tells that the caller read the account
// itself a moment ago; it is then not asked for a second time.
func (m *Manager) admit(ctx context.Context, userID int, provider, reconnect string, loaded bool) (client Client, renew *Reconnect, err error) {
	if m.specs[provider] == nil {
		return client, nil, serializer.NewError(serializer.CodeParamErr, "Unknown provider", nil)
	}
	client, ok, err := m.client(ctx, provider)
	if err != nil {
		return client, nil, err
	}
	if !ok {
		return client, nil, reasoned(serializer.CodeConnectedUnavailable, "The provider is not set up", ReasonNotConfigured)
	}
	if m.leaving[userID] > 0 || (!loaded && m.exists != nil && !m.exists(ctx, userID)) {
		return client, nil, flowError(ErrFlowExpired)
	}

	if reconnect != "" {
		row, err := m.row(ctx, userID, reconnect)
		if err != nil {
			return client, nil, err
		}
		if row.Provider != provider {
			return client, nil, serializer.NewError(serializer.CodeParamErr, "The storage is at another provider", nil)
		}
		return client, &Reconnect{ID: row.PublicID, Generation: row.Generation}, nil
	}
	rows, err := m.storages.ListByUser(ctx, userID)
	if err != nil {
		return client, nil, serializer.NewError(serializer.CodeDBError, "Failed to list connected storages", err)
	}
	if len(rows) >= MaxPerUser {
		return client, nil, reasoned(serializer.CodeParamErr, "Too many connected storages", ReasonTooMany)
	}
	return client, nil, nil
}

// startError is the answer of the API to a sign-in that could not be kept.
func startError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrFlowWait):
		return ErrBusy
	}
	return serializer.NewError(serializer.CodeParamErr, "Failed to start the sign-in", err)
}

// Authorize starts connecting a storage, or renewing the token of the connection
// reconnect. redirect is the callback address of this installation as registered at the
// provider. It returns the consent address and the value for the browser's cookie.
func (m *Manager) Authorize(ctx context.Context, userID int, provider, reconnect, redirect string) (address, cookie string, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, renew, err := m.admit(ctx, userID, provider, reconnect, false)
	if err != nil {
		return "", "", err
	}
	client.Redirect = redirect
	address, cookie, err = m.flow.Begin(userID, provider, client, renew)
	return address, cookie, startError(err)
}

// AuthorizeApp keeps a sign-in that an app asked for, under the same checks (see
// oauth_app.go). challenge is the hash of the secret the app keeps. It returns the
// ticket, with which the app sends the system browser to the start page, and the id the
// app knows the sign-in by. Nothing is at the provider yet.
func (m *Manager) AuthorizeApp(ctx context.Context, userID int, provider, reconnect, challenge string) (ticket, attempt string, err error) {
	if !ValidChallenge(challenge) {
		return "", "", serializer.NewError(serializer.CodeParamErr, "Invalid challenge", nil)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, renew, err := m.admit(ctx, userID, provider, reconnect, false)
	if err != nil {
		return "", "", err
	}
	ticket, attempt, err = m.flow.Ticket(userID, provider, renew, challenge)
	return ticket, attempt, startError(err)
}

// OwnsTicket tells whether a browser shows the cookie of the one that owns a ticket.
func (m *Manager) OwnsTicket(ticket, cookie string) bool {
	_, err := m.flow.Check(ticket, cookie)
	return err == nil
}

// OpenTicket answers a browser that asks for the start page of a ticket (see Flow.Open).
func (m *Manager) OpenTicket(ticket, cookie string) (view *TicketView, confirm string, err error) {
	return m.flow.Open(ticket, cookie)
}

// TicketRefused tells that a ticket cannot become a sign-in and is used up. Reason is
// the word the app is told.
type TicketRefused struct{ Reason string }

func (e *TicketRefused) Error() string { return "the sign-in cannot start: " + e.Reason }

// refusal names why the checks before a sign-in failed for good, "" for trouble that
// may pass (the database could not be read).
func refusal(err error) string {
	var app serializer.AppError
	if errors.As(err, &app) && app.Code == serializer.CodeDBError {
		return ""
	}
	switch reason := ReasonOf(err); reason {
	case ReasonNotAllowed, ReasonTooMany:
		return reason
	case ReasonNotConfigured:
		return ReasonSetup
	}
	// The account or the connection to renew is gone.
	return ReasonExpired
}

// StartTicket turns a ticket into a sign-in at the provider, for the browser that owns
// the ticket. Everything a sign-in is checked for when it is asked for is checked again
// now, minutes later: allowed tells whether the account still signs in and may connect
// storages (nil, ErrNotAllowed, ErrFlowExpired for an account that is gone or blocked,
// or an error of the database). It returns the consent address and the value of the
// sign-in cookie; the same again for the same browser while that sign-in is under way.
// One ticket at a time goes through here: of two presses at once the second finds what
// the first made, and never decides differently about the same ticket.
//
// The errors: ErrFlowExpired, without a view, when the ticket is not one of this
// browser; ErrFlowWait when it can be tried again in a moment, the ticket stays; a
// *TicketRefused when the checks failed, the ticket is used up.
func (m *Manager) StartTicket(ctx context.Context, ticket, cookie, redirect string,
	allowed func(ctx context.Context, userID int) error) (address, signIn string, view *TicketView, err error) {
	m.starting.Lock()
	defer m.starting.Unlock()
	m.mu.RLock()
	defer m.mu.RUnlock()
	view, err = m.flow.Check(ticket, cookie)
	if err != nil {
		return "", "", nil, ErrFlowExpired
	}

	var client Client
	if !view.Used {
		reconnect := ""
		if view.Reconnect != nil {
			reconnect = view.Reconnect.ID
		}
		var renew *Reconnect
		err := allowed(ctx, view.UserID)
		if errors.Is(err, ErrFlowExpired) {
			err = flowError(ErrFlowExpired)
		}
		if err == nil {
			client, renew, err = m.admit(ctx, view.UserID, view.Provider, reconnect, true)
		}
		if err == nil && renew != nil && renew.Generation != view.Reconnect.Generation {
			// Connected again meanwhile, by another sign-in.
			err = flowError(ErrFlowExpired)
		}
		if err != nil {
			reason := refusal(err)
			if reason == "" {
				return "", "", view, ErrFlowWait
			}
			m.flow.Drop(ticket)
			return "", "", view, &TicketRefused{Reason: reason}
		}
		client.Redirect = redirect
	}

	address, signIn, err = m.flow.Start(ticket, cookie, client)
	if err != nil && !errors.Is(err, ErrFlowWait) {
		return "", "", nil, ErrFlowExpired
	}
	return address, signIn, view, err
}

// Callback handles the return from the provider. Its outcome carries the code for
// Finish; the error is nil or one of the ErrFlow errors.
func (m *Manager) Callback(ctx context.Context, state, cookie, code, providerError string) (Outcome, error) {
	return m.flow.Callback(ctx, state, cookie, code, providerError)
}

// ProviderName is the name a provider shows under, "" for one this version has not.
func (m *Manager) ProviderName(provider string) string {
	if spec := m.specs[provider]; spec != nil {
		return spec.Name
	}
	return ""
}

// Get returns one connection of an account.
func (m *Manager) Get(ctx context.Context, userID int, publicID string) (*Storage, error) {
	row, err := m.row(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	return storageOf(row), nil
}

// FlowErrorCode names an error of the sign-in for the web app, which shows a text for it.
func FlowErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrFlowExpired):
		return "expired"
	case errors.Is(err, ErrFlowBrowser):
		return "browser"
	case errors.Is(err, ErrFlowDenied):
		return "denied"
	case errors.Is(err, ErrFlowSetup):
		return "setup"
	case errors.Is(err, ErrFlowAccount):
		return "account"
	case errors.Is(err, ErrFlowVerifier):
		return "verifier"
	case errors.Is(err, ErrFlowOvertaken):
		return "overtaken"
	}
	return "provider"
}

func flowError(err error) error {
	code := serializer.CodeParamErr
	if errors.Is(err, ErrFlowSetup) {
		code = serializer.CodeConnectedUnavailable
	}
	// The texts of these errors are fixed, nothing of a provider's answer is in them.
	return reasoned(code, err.Error(), FlowErrorCode(err))
}

func tokenContext(publicID string, userID int, provider string) []byte {
	return Context(publicID, strconv.Itoa(userID), provider)
}

// SignedIn is what an app is shown of the result of its sign-in before it stores it.
type SignedIn struct {
	Provider string `json:"provider"`
	// Account names the account at the provider, AccountKind what kind of name that is
	// (see the Account* kinds).
	Account     string `json:"account"`
	AccountKind string `json:"account_kind"`
	// Reconnect is the id of the storage the sign-in renews.
	Reconnect string `json:"reconnect,omitempty"`
}

// Preview shows the result of a sign-in from an app to the account and the verifier it
// is for. Nothing is stored and the result stays.
func (m *Manager) Preview(userID int, code, verifier string) (*SignedIn, error) {
	granted, err := m.flow.Peek(userID, code, verifier)
	if err != nil {
		return nil, flowError(err)
	}
	res := &SignedIn{Provider: granted.Provider, Account: granted.Identity.Shown, AccountKind: granted.Identity.Kind}
	if res.AccountKind == "" || res.Account == "" {
		res.Account, res.AccountKind = "", AccountNone
	}
	if granted.Reconnect != nil {
		res.Reconnect = granted.Reconnect.ID
	}
	return res, nil
}

// Cancel drops the result of a sign-in from an app, for the account and the verifier it
// is for.
func (m *Manager) Cancel(userID int, code, verifier string) error {
	if err := m.flow.Cancel(userID, code, verifier); err != nil {
		return flowError(err)
	}
	return nil
}

// Finish stores the result of a sign-in for the account that started it. The result of a
// sign-in from an app needs the verifier of that app; one of the web app comes without.
// An app that asks again for a result it stored a moment ago gets the same storage.
func (m *Manager) Finish(ctx context.Context, userID int, code, verifier string) (*Storage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.leaving[userID] > 0 {
		// The account is being deleted. The result of the sign-in is left to expire.
		return nil, flowError(ErrFlowExpired)
	}

	granted, err := m.flow.Finish(userID, code, verifier)
	if errors.Is(err, ErrFlowExpired) {
		if id, ok := m.flow.Receipt(userID, code, verifier); ok {
			// The answer of the first time was lost. The storage is read again.
			return m.Get(ctx, userID, id)
		}
	}
	if err != nil {
		return nil, flowError(err)
	}
	// The receipt is written the moment the storage is kept, also when something after
	// that fails: the app that asks again then gets the storage instead of "expired".
	return m.store(ctx, userID, granted, func(id string) { m.flow.Remember(code, granted, id) })
}

// store keeps the token of a finished sign-in: for the connection it renews, or as a new
// connection. kept is told the id of the storage once it is stored for good. The caller
// holds m.mu.
func (m *Manager) store(ctx context.Context, userID int, granted *Granted, kept func(id string)) (*Storage, error) {
	props := &types.ConnectedStorageProps{
		DriveID:   granted.Identity.Props["drive_id"],
		DriveType: granted.Identity.Props["drive_type"],
		Scopes:    granted.Scopes,
	}

	if granted.Reconnect != nil {
		row, err := m.row(ctx, userID, granted.Reconnect.ID)
		if err != nil {
			return nil, err
		}
		if row.AccountKey != granted.Identity.Key {
			return nil, reasoned(serializer.CodeParamErr,
				"This is another account at the provider than the one the storage belongs to", ReasonOtherAccount)
		}
		secret, err := m.sealer.Seal(ctx, PurposeToken, tokenContext(row.PublicID, userID, row.Provider), []byte(granted.Token))
		if err != nil {
			return nil, serializer.NewError(serializer.CodeInternalSetting, "Failed to seal the token", err)
		}
		if row.Props != nil {
			props.View = row.Props.View
		}
		renewed, ok, err := m.storages.Renew(ctx, row.ID, granted.Reconnect.Generation, &inventory.RenewConnectedStorageArgs{
			Account: granted.Identity.Label,
			Secret:  secret,
			Props:   props,
		})
		if ok {
			// The new token is stored, whatever becomes of reading the row back.
			kept(row.PublicID)
			// What ran with the old token ends here, whatever else went wrong.
			m.closeLive(row.PublicID)
			if m.hooks.Closed != nil {
				m.hooks.Closed(row.PublicID, granted.Reconnect.Generation)
			}
		}
		if err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to store the token", err)
		}
		if !ok {
			// Connected again meanwhile, by another sign-in. An app is told so in its
			// own word; the web app goes on getting the one it knows.
			if granted.App {
				return nil, flowError(ErrFlowOvertaken)
			}
			return nil, flowError(ErrFlowExpired)
		}
		return storageOf(renewed), nil
	}

	storage, err := m.createLocked(ctx, userID, granted.Provider, granted.Identity, granted.Token, props)
	if err == nil {
		kept(storage.ID)
	}
	return storage, err
}

// Create stores a new connection of an account from a token its provider granted. The
// sign-in flow ends here; other callers are tests and tools that hold such a token.
func (m *Manager) Create(ctx context.Context, userID int, kind string, identity *Identity, token string) (*Storage, error) {
	if m.specs[kind] == nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "Unknown provider", nil)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLocked(ctx, userID, kind, identity, token, &types.ConnectedStorageProps{
		DriveID:   identity.Props["drive_id"],
		DriveType: identity.Props["drive_type"],
	})
}

// createLocked stores a new connection. The caller holds m.mu.
func (m *Manager) createLocked(ctx context.Context, userID int, kind string, identity *Identity, token string,
	props *types.ConnectedStorageProps) (*Storage, error) {
	if m.leaving[userID] > 0 {
		return nil, flowError(ErrFlowExpired)
	}
	// A request can have been let in for an account that was deleted since: nothing is
	// stored for an account that is gone.
	if m.exists != nil && !m.exists(ctx, userID) {
		return nil, flowError(ErrFlowExpired)
	}
	rows, err := m.storages.ListByUser(ctx, userID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list connected storages", err)
	}
	if len(rows) >= MaxPerUser {
		return nil, reasoned(serializer.CodeParamErr, "Too many connected storages", ReasonTooMany)
	}
	for _, row := range rows {
		if row.Provider == kind && row.AccountKey == identity.Key {
			return nil, reasoned(serializer.CodeObjectExist, "This account is already connected", ReasonExists)
		}
	}

	publicID := newPublicID()
	secret, err := m.sealer.Seal(ctx, PurposeToken, tokenContext(publicID, userID, kind), []byte(token))
	if err != nil {
		return nil, serializer.NewError(serializer.CodeInternalSetting, "Failed to seal the token", err)
	}
	row, err := m.storages.Create(ctx, &inventory.CreateConnectedStorageArgs{
		UserID:     userID,
		PublicID:   publicID,
		Provider:   kind,
		Name:       m.specs[kind].Name,
		Account:    identity.Label,
		AccountKey: identity.Key,
		Secret:     secret,
		Props:      props,
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to store the connected storage", err)
	}
	return storageOf(row), nil
}

// Rename changes the name of a connection.
func (m *Manager) Rename(ctx context.Context, userID int, publicID, name string) (*Storage, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid name", nil)
	}
	row, err := m.row(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	renamed, err := m.storages.Rename(ctx, row.ID, name)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to rename the connected storage", err)
	}
	return storageOf(renamed), nil
}

// Disconnect ends a connection. The row with its token is deleted first; what follows is
// clean-up that cannot bring it back.
func (m *Manager) Disconnect(ctx context.Context, userID int, publicID string) error {
	revoke, err := m.disconnect(ctx, userID, publicID)
	if err != nil {
		return err
	}
	m.takeBack(ctx, revoke)
	return nil
}

func (m *Manager) disconnect(ctx context.Context, userID int, publicID string) (revoke, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Read under the lock: a sign-in that renews the connection at the same moment has
	// either stored its token before, and that generation ends here, or finds no row.
	row, err := m.row(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	if err := m.storages.Delete(ctx, row.ID); err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to delete the connected storage", err)
	}
	return m.afterDelete(ctx, row), nil
}

// DisconnectUser ends all connections of an account that is being removed, and the
// sign-ins it has under way.
func (m *Manager) DisconnectUser(ctx context.Context, userID int) error {
	m.mu.Lock()
	m.flow.Forget(userID)
	rows, err := m.storages.DeleteByUser(ctx, userID)
	revokes := make([]revoke, 0, len(rows))
	for _, row := range rows {
		revokes = append(revokes, m.afterDelete(ctx, row))
	}
	m.mu.Unlock()
	if err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to delete connected storages", err)
	}
	m.takeBack(ctx, revokes...)
	return nil
}

// revoke takes the access of a deleted connection back at its provider.
type revoke func(ctx context.Context)

// takeBack runs revokes, which may be nil, after the lock of the manager was released:
// a provider that is slow to answer must not hold up every other request. All of them
// together get ten seconds; it is best effort, the tokens are gone from here either way.
func (m *Manager) takeBack(ctx context.Context, revokes ...revoke) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, r := range revokes {
		if r != nil {
			r(ctx)
		}
	}
}

// afterDelete cleans up after a row was deleted. The caller holds m.mu, and runs what is
// returned, if anything, once it has released it.
func (m *Manager) afterDelete(ctx context.Context, row *ent.ConnectedStorage) revoke {
	m.closeLive(row.PublicID)
	// The previews kept of its files go with the connection.
	m.thumbs.drop(row.PublicID)
	if m.hooks.Closed != nil {
		m.hooks.Closed(row.PublicID, row.Generation)
	}

	// Taking the access back at the provider ends it for every connection to that
	// provider account, also one that is being set up right now. So only when there is
	// no other, and none can come up before the provider was told.
	spec := m.specs[row.Provider]
	if spec == nil || spec.Revoke == nil {
		return nil
	}
	if n, err := m.storages.CountByAccount(ctx, row.Provider, row.AccountKey); err != nil || n > 0 {
		return nil
	}
	token, err := m.sealer.Open(ctx, PurposeToken, tokenContext(row.PublicID, row.UserID, row.Provider), row.Secret)
	if err != nil {
		return nil
	}
	stored, err := ParseToken(string(token))
	if err != nil || stored.RefreshToken == "" {
		return nil
	}
	done, ok := m.flow.Revoking(row.Provider)
	if !ok {
		return nil
	}
	return func(ctx context.Context) {
		defer done()
		_ = spec.Revoke(ctx, m.http, stored.RefreshToken)
	}
}
