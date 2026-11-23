// Package provider is what connected storages need from a storage provider.
//
// Every call names items by the id the provider gave them, never by a path: a name
// is not an identity (Google Drive allows several entries with one name in a folder),
// and a path can stand for another item a moment later. Paths exist only in the
// package above, which walks them down to ids.
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Item is a file, a folder or a link in a folder at the provider.
type Item struct {
	// ID is the id of the entry that sits in the folder. For a link it is the id of
	// the link, not of what it points to.
	ID   string
	Name string
	// IsDir tells whether the entry is, or links to, a folder.
	IsDir   bool
	Size    int64
	ModTime time.Time
	// Version changes whenever the content does. Empty for folders.
	Version string
	// Revision names the stored content of a version for a read of exactly that
	// content, where the provider has such a thing. May be empty.
	Revision string
	// Hash is the provider's checksum of the content (Drive: MD5 in hex; OneDrive:
	// QuickXorHash in base64). May be empty.
	Hash string
	// ETag is the provider's tag for conditional requests on the item. May be empty.
	ETag string
	// Native is set for a document that exists only at the provider and has no bytes
	// of its own: "gdoc", "gsheet", "gslides" or "gdraw".
	Native string
	// WebURL opens the item at the provider.
	WebURL string
	// ResourceKey is what some items of Google Drive need, besides their id, to be
	// reached. Empty for all others.
	ResourceKey string
	// Link is set for an entry that points to another item: a Drive shortcut or a
	// OneDrive item of another drive.
	Link *Link
}

// Link is where a link points to.
type Link struct {
	// TargetID is the id of the item the link points to.
	TargetID string
	// TargetDrive is the drive of the target when it is not the connection's own.
	TargetDrive string
	// ResourceKey is needed by some Drive targets to be opened.
	ResourceKey string
}

// ContentID is the id of the item whose bytes or children the entry shows.
func (i *Item) ContentID() string {
	if i.Link != nil {
		return i.Link.TargetID
	}
	return i.ID
}

// Page is one page of the entries of a folder.
type Page struct {
	Items []Item
	// Next asks for the following page; empty on the last one.
	Next string
}

// Content is an open download.
type Content struct {
	Body io.ReadCloser
	// Partial tells that Body is the range that was asked for.
	Partial bool
	// Length of Body in bytes, -1 when unknown.
	Length int64
	// ContentRange is the Content-Range value of a partial answer.
	ContentRange string
}

// Usage is the fill level of the storage. Total is 0 when the provider does not tell.
type Usage struct {
	Used  int64
	Total int64
}

// Provider is one storage provider, for one connection.
type Provider interface {
	// Root returns the id of the root folder of the connection's drive.
	Root(ctx context.Context) (string, error)
	// Children returns one page of the entries of a folder. page is empty for the
	// first page, later the Next of the page before. Trashed entries are left out. A
	// page the provider marks as incomplete is an error of kind KindIncomplete.
	Children(ctx context.Context, folderID, page string) (*Page, error)
	// Named returns the entries of a folder whose name is exactly name (on a provider
	// that ignores case: without regard to case). Trashed entries are left out.
	Named(ctx context.Context, folderID, name string) ([]Item, error)
	// Get returns the entry with the given id, KindNotFound when it is gone or trashed.
	Get(ctx context.Context, id string) (*Item, error)
	// Known tells the provider about an entry it handed out earlier, before calls
	// that name the entry by its id alone. A provider that needs more than the id to
	// reach an item keeps that from it; the others do nothing.
	Known(item *Item)
	// Target returns link, an entry that points to another item, with the size, time,
	// version, revision and checksum of that item as it is now. A listing does not
	// tell these. KindNotFound when the target is gone.
	Target(ctx context.Context, link *Item) (*Item, error)
	// Open starts a download of the content of item, which came from Children, Named,
	// Get or Target of this provider. rangeHeader is an HTTP Range value or empty.
	// Where the provider can serve item.Revision, exactly that content is read.
	Open(ctx context.Context, item *Item, rangeHeader string) (*Content, error)
	// Export starts a download of a native document in the given format ("docx",
	// "xlsx", "pptx", "pdf", "svg").
	Export(ctx context.Context, item *Item, format string) (*Content, error)
	// About returns the fill level of the storage.
	About(ctx context.Context) (*Usage, error)
	// CaseInsensitive tells whether names in a folder are unique without regard to case.
	CaseInsensitive() bool
}

// Kind classifies an error of a provider.
type Kind int

const (
	KindOther Kind = iota
	// KindNotFound: the item does not exist (any more) or is in the trash.
	KindNotFound
	// KindExists: the name is taken.
	KindExists
	// KindReconnect: the provider refuses the stored access; the account has to sign in again.
	KindReconnect
	// KindSetup: the provider refuses the OAuth client of the installation.
	KindSetup
	// KindBusy: rate limit, timeout or a provider that does not answer.
	KindBusy
	// KindFull: the storage at the provider is full.
	KindFull
	// KindForbidden: the account may not do this with this item.
	KindForbidden
	// KindFlagged: the provider blocks the file (malware or abuse).
	KindFlagged
	// KindExportTooLarge: the document is too large to be exported.
	KindExportTooLarge
	// KindDailyLimit: a daily limit of the provider is reached.
	KindDailyLimit
	// KindStale: the item is not in the state the request named (a refused condition,
	// or a version the provider no longer serves).
	KindStale
	// KindIncomplete: the provider could not answer a listing completely.
	KindIncomplete
	// KindUnsafe: an address handed out by the provider was refused.
	KindUnsafe
)

// Error is an error of a provider. It never carries the provider's text: Code is the
// provider's own short code for the error (a word like "notFound"), safe to log.
type Error struct {
	Kind   Kind
	Status int
	Code   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("provider error (kind %d, status %d, code %q)", e.Kind, e.Status, e.Code)
}

// KindOf returns the kind of a provider error, KindOther for everything else.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindOther
}

// Refusable is an Auth that wants to know when the provider refused a token it had
// handed out a moment before: no further token will help.
type Refusable interface {
	Refused(ctx context.Context, token string)
}

// Auth supplies the access token of a connection.
type Auth interface {
	// Token returns an access token to use now. rejected is a token the provider just
	// answered 401 to, or empty: when it is still the current one it is replaced first.
	// A refused refresh is an error of kind KindReconnect or KindSetup.
	Token(ctx context.Context, rejected string) (string, error)
}
