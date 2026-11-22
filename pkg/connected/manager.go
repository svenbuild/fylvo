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
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

const (
	// maxPerUser is the most storages one account can connect.
	maxPerUser = 10
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
	// HTTP talks to the providers; nil takes a client with a timeout.
	HTTP  *http.Client
	Hooks Hooks
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

	// mu orders the steps that decide over tokens of a provider account: storing the
	// result of a sign-in, disconnecting and revoking.
	mu sync.Mutex
	// leaving counts, per account, the deletions of that account that are under way.
	leaving map[int]int
}

func NewManager(o ManagerOptions) *Manager {
	if o.Specs == nil {
		o.Specs = Specs()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	return &Manager{
		storages: o.Storages,
		settings: o.Settings,
		sealer:   o.Sealer,
		specs:    o.Specs,
		http:     o.HTTP,
		flow:     NewFlow(o.Specs, o.HTTP),
		hooks:    o.Hooks,
		leaving:  map[int]int{},
	}
}

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

// Authorize starts connecting a storage, or renewing the token of the connection
// reconnect. redirect is the callback address of this installation as registered at the
// provider. It returns the consent address and the value for the browser's cookie.
func (m *Manager) Authorize(ctx context.Context, userID int, provider, reconnect, redirect string) (address, cookie string, err error) {
	if m.specs[provider] == nil {
		return "", "", serializer.NewError(serializer.CodeParamErr, "Unknown provider", nil)
	}
	client, ok, err := m.client(ctx, provider)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", ErrUnavailable
	}
	client.Redirect = redirect

	var renew *Reconnect
	if reconnect != "" {
		row, err := m.row(ctx, userID, reconnect)
		if err != nil {
			return "", "", err
		}
		if row.Provider != provider {
			return "", "", serializer.NewError(serializer.CodeParamErr, "The storage is at another provider", nil)
		}
		renew = &Reconnect{ID: row.PublicID, Generation: row.Generation}
	} else {
		rows, err := m.storages.ListByUser(ctx, userID)
		if err != nil {
			return "", "", serializer.NewError(serializer.CodeDBError, "Failed to list connected storages", err)
		}
		if len(rows) >= maxPerUser {
			return "", "", serializer.NewError(serializer.CodeParamErr, "Too many connected storages", nil)
		}
	}

	address, cookie, err = m.flow.Begin(userID, provider, client, renew)
	if err != nil {
		return "", "", serializer.NewError(serializer.CodeParamErr, "Failed to start the sign-in", err)
	}
	return address, cookie, nil
}

// Callback handles the return from the provider. It returns the code for Finish, or one
// of the ErrFlow errors.
func (m *Manager) Callback(ctx context.Context, state, cookie, code, providerError string) (string, error) {
	return m.flow.Callback(ctx, state, cookie, code, providerError)
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
	}
	return "provider"
}

func flowError(err error) error {
	code := serializer.CodeParamErr
	if errors.Is(err, ErrFlowSetup) {
		code = serializer.CodeConnectedUnavailable
	}
	// The texts of these errors are fixed, nothing of a provider's answer is in them.
	return serializer.NewError(code, err.Error(), nil)
}

func tokenContext(publicID string, userID int, provider string) []byte {
	return Context(publicID, strconv.Itoa(userID), provider)
}

// Finish stores the result of a sign-in for the account that started it.
func (m *Manager) Finish(ctx context.Context, userID int, code string) (*Storage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.leaving[userID] > 0 {
		// The account is being deleted. The result of the sign-in is left to expire.
		return nil, flowError(ErrFlowExpired)
	}

	granted, err := m.flow.Finish(userID, code)
	if err != nil {
		return nil, flowError(err)
	}

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
			return nil, serializer.NewError(serializer.CodeParamErr,
				"This is another account at the provider than the one the storage belongs to", nil)
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
		if err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to store the token", err)
		}
		if !ok {
			// Connected again meanwhile, by another sign-in.
			return nil, flowError(ErrFlowExpired)
		}
		if m.hooks.Closed != nil {
			m.hooks.Closed(row.PublicID, granted.Reconnect.Generation)
		}
		return storageOf(renewed), nil
	}

	rows, err := m.storages.ListByUser(ctx, userID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list connected storages", err)
	}
	if len(rows) >= maxPerUser {
		return nil, serializer.NewError(serializer.CodeParamErr, "Too many connected storages", nil)
	}
	for _, row := range rows {
		if row.Provider == granted.Provider && row.AccountKey == granted.Identity.Key {
			return nil, serializer.NewError(serializer.CodeObjectExist, "This account is already connected", nil)
		}
	}

	publicID := newPublicID()
	secret, err := m.sealer.Seal(ctx, PurposeToken, tokenContext(publicID, userID, granted.Provider), []byte(granted.Token))
	if err != nil {
		return nil, serializer.NewError(serializer.CodeInternalSetting, "Failed to seal the token", err)
	}
	row, err := m.storages.Create(ctx, &inventory.CreateConnectedStorageArgs{
		UserID:     userID,
		PublicID:   publicID,
		Provider:   granted.Provider,
		Name:       m.specs[granted.Provider].Name,
		Account:    granted.Identity.Label,
		AccountKey: granted.Identity.Key,
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
	m.mu.Lock()
	defer m.mu.Unlock()

	// Read under the lock: a sign-in that renews the connection at the same moment has
	// either stored its token before, and that generation ends here, or finds no row.
	row, err := m.row(ctx, userID, publicID)
	if err != nil {
		return err
	}
	if err := m.storages.Delete(ctx, row.ID); err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to delete the connected storage", err)
	}
	m.afterDelete(ctx, row)
	return nil
}

// DisconnectUser ends all connections of an account that is being removed.
func (m *Manager) DisconnectUser(ctx context.Context, userID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := m.storages.DeleteByUser(ctx, userID)
	if err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to delete connected storages", err)
	}
	for _, row := range rows {
		m.afterDelete(ctx, row)
	}
	return nil
}

// afterDelete cleans up after a row was deleted. The caller holds m.mu.
func (m *Manager) afterDelete(ctx context.Context, row *ent.ConnectedStorage) {
	if m.hooks.Closed != nil {
		m.hooks.Closed(row.PublicID, row.Generation)
	}

	// Taking the access back at the provider ends it for every connection to that
	// provider account, also one that is being set up right now. So only when there is
	// no other, and best effort: the token is gone from here either way.
	spec := m.specs[row.Provider]
	if spec == nil || spec.Revoke == nil || m.flow.Busy(row.Provider) {
		return
	}
	if n, err := m.storages.CountByAccount(ctx, row.Provider, row.AccountKey); err != nil || n > 0 {
		return
	}
	token, err := m.sealer.Open(ctx, PurposeToken, tokenContext(row.PublicID, row.UserID, row.Provider), row.Secret)
	if err != nil {
		return
	}
	stored, err := ParseToken(string(token))
	if err != nil || stored.RefreshToken == "" {
		return
	}
	revokeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = spec.Revoke(revokeCtx, m.http, stored.RefreshToken)
}
