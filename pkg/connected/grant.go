package connected

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// Kinds of grants.
const (
	GrantContent = "c"
	GrantThumb   = "t"
)

// ErrGrant is returned for a grant that cannot be read or has run out. It says nothing
// about which.
var ErrGrant = errors.New("grant is not valid")

// Grant is the right to read one item of a connected storage for a while. Sealed, it is
// part of the address of a download, so it must name everything the download depends
// on: the URL signer of the server covers only the path of an address, and a grant that
// left the choice of the item to a parameter would open every file of the connection.
type Grant struct {
	Kind string `json:"k"`
	// User and Fingerprint: the account the grant was made for, as it was then. A new
	// password ends the grant.
	User        int    `json:"u"`
	Fingerprint string `json:"f"`
	// Conn and Generation: the connection as it was. A new token ends the grant.
	Conn       string `json:"c"`
	Generation int    `json:"g"`
	// Item is the entry to read: its own id, what it links to, and the state of the
	// content the grant is for.
	Item GrantItem `json:"i"`
	// Export is the format a document that exists only at the provider is read in.
	Export   string `json:"x,omitempty"`
	Download bool   `json:"d,omitempty"`
	Expires  int64  `json:"e"`
}

// GrantItem is the part of a provider item a grant keeps.
type GrantItem struct {
	ID       string `json:"i"`
	Name     string `json:"n"`
	Version  string `json:"v,omitempty"`
	Revision string `json:"r,omitempty"`
	Size     int64  `json:"s,omitempty"`
	Native   string `json:"a,omitempty"`
	Target   string `json:"t,omitempty"`
	Drive    string `json:"d,omitempty"`
	Key      string `json:"k,omitempty"`
	Link     bool   `json:"l,omitempty"`
	// Own is the resource key of the entry itself, where it has one.
	Own string `json:"o,omitempty"`
}

// GrantItemOf keeps what a grant needs of a row.
func GrantItemOf(row *Row) GrantItem {
	g := GrantItem{ID: row.ID, Name: row.Name, Version: row.Version, Revision: row.Revision, Size: row.Size, Native: row.Native, Own: row.ResourceKey}
	if row.Link != nil {
		g.Link, g.Target, g.Drive, g.Key = true, row.Link.TargetID, row.Link.TargetDrive, row.Link.ResourceKey
	}
	return g
}

// ProviderItem turns the item of a grant back into what a provider reads.
func (g *GrantItem) ProviderItem() *provider.Item {
	it := &provider.Item{ID: g.ID, Name: g.Name, Version: g.Version, Revision: g.Revision, Size: g.Size, Native: g.Native, ResourceKey: g.Own}
	if g.Link {
		it.Link = &provider.Link{TargetID: g.Target, TargetDrive: g.Drive, ResourceKey: g.Key}
	}
	return it
}

var grantContext = Context("grant")

// SealGrant returns the grant in the form that goes into an address.
func (m *Manager) SealGrant(ctx context.Context, g *Grant) (string, error) {
	plain, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	sealed, err := m.sealer.Seal(ctx, PurposeGrant, grantContext, plain)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// SealGrantStable is SealGrant with an address part that is the same every time for the
// same grant, so a client can keep what it loaded from that address.
func (m *Manager) SealGrantStable(ctx context.Context, g *Grant) (string, error) {
	plain, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	sealed, err := m.sealer.SealStable(ctx, PurposeGrant, grantContext, plain)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// OpenGrant reads a sealed grant and refuses one that has run out.
func (m *Manager) OpenGrant(ctx context.Context, sealed string) (*Grant, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return nil, ErrGrant
	}
	plain, err := m.sealer.Open(ctx, PurposeGrant, grantContext, raw)
	if err != nil {
		return nil, ErrGrant
	}
	var g Grant
	if err := json.Unmarshal(plain, &g); err != nil || time.Now().Unix() > g.Expires {
		return nil, ErrGrant
	}
	return &g, nil
}

// maxStreams is how many downloads from providers run at once on this server.
const maxStreams = 8

var streams = make(chan struct{}, maxStreams)

// AcquireStream takes one of the download slots, waiting a short while for one. The
// returned function gives it back.
func AcquireStream(ctx context.Context) (release func(), err error) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case streams <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-streams }) }, nil
	case <-timer.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ExportFormats are the formats a document that exists only at the provider can be read
// in, the first one being what a plain download gives.
var ExportFormats = map[string][]string{
	"gdoc":    {"docx", "pdf"},
	"gsheet":  {"xlsx", "pdf"},
	"gslides": {"pptx", "pdf"},
	"gdraw":   {"svg", "pdf"},
}

// ExportName is the file name of a native document read in the given format.
func ExportName(name, native, format string) string {
	return strings.TrimSuffix(name, "."+native) + "." + format
}

// View returns the view setting kept for a connection, nil when there is none.
func (m *Manager) View(ctx context.Context, userID int, publicID string) (*types.ExplorerView, error) {
	row, err := m.row(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	if row.Props == nil {
		return nil, nil
	}
	return row.Props.View, nil
}

// SetView stores the view setting of a connection in the given generation; nil removes it.
func (m *Manager) SetView(ctx context.Context, userID int, publicID string, generation int, view *types.ExplorerView) error {
	// Not while the connection is connected again: that step carries the view over to
	// the new generation, and must not carry over one that is just being replaced.
	m.mu.RLock()
	defer m.mu.RUnlock()

	row, err := m.row(ctx, userID, publicID)
	if err != nil {
		return err
	}
	if row.Generation != generation {
		// Connected again since the request was let in: it is not for this one.
		return ErrBusy
	}
	props := types.ConnectedStorageProps{}
	if row.Props != nil {
		props = *row.Props
	}
	props.View = view
	saved, err := m.storages.UpdateProps(ctx, row.ID, row.Generation, &props)
	if err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to save the view", err)
	}
	if !saved {
		// Connected again at this very moment: the view of the new generation was not touched.
		return ErrBusy
	}
	return nil
}

// usage is the fill level of a connection as it was read a short while ago.
type usage struct {
	at time.Time
	u  provider.Usage
}

// Capacity returns the fill level of the storage of a connection. The provider is asked
// at most every five minutes.
func (m *Manager) Capacity(ctx context.Context, c *Conn) (*provider.Usage, error) {
	key := c.ID + "#" + strconv.Itoa(c.Generation)
	m.liveMu.Lock()
	cached, ok := m.usage[key]
	m.liveMu.Unlock()
	if ok && time.Since(cached.at) < 5*time.Minute {
		return &cached.u, nil
	}

	u, err := c.P.About(ctx)
	if err != nil {
		return nil, err
	}
	m.liveMu.Lock()
	// Not for a connection that ended while the provider answered.
	if m.live[c.ID] == c.live {
		m.usage[key] = usage{at: time.Now(), u: *u}
	}
	m.liveMu.Unlock()
	return u, nil
}
