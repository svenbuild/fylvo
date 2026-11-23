package provider

import (
	"context"
	"io"
)

// Writer is what a provider offers for changing a storage. Like every call of a
// provider, these name items by id. None of them looks at names that are already
// there unless it says so: the package above checks names before it calls.
type Writer interface {
	// CreateFolder creates a folder named name in the folder parentID and returns it.
	// On a provider with unique names a taken name is KindExists; nothing is replaced
	// or merged.
	CreateFolder(ctx context.Context, parentID, name string) (*Item, error)
	// CreateEmpty creates a file without content, under the same rules.
	CreateEmpty(ctx context.Context, parentID, name string) (*Item, error)
	// Update renames an item, moves it, or both, in one call, and returns it as it is
	// afterwards. On a provider with unique names a taken name is KindExists.
	Update(ctx context.Context, id string, change Change) (*Item, error)
	// Trash moves an item into the provider's trash. An item that is already gone is
	// KindNotFound.
	Trash(ctx context.Context, id string) error

	// NewUpload opens an upload of size bytes (more than 0) as a new file named name in
	// the folder parentID. On a provider with unique names a taken name is KindExists,
	// now or when the last range is sent.
	NewUpload(ctx context.Context, parentID, name string, size int64) (Upload, error)
	// ReplaceUpload opens an upload of size bytes (more than 0) that replaces the
	// content of item, which came from Get just before. Where the provider can make the
	// change depend on the state of the item (item.ETag), it does; a refused condition
	// is KindStale, now or when the last range is sent.
	ReplaceUpload(ctx context.Context, item *Item, size int64) (Upload, error)
}

// Change is what Update changes. A zero field stays as it is.
type Change struct {
	// Name is the new name.
	Name string
	// Parent is the id of the folder the item moves to; OldParent is the id of the
	// folder it is in now, which some providers need to take it out of there.
	Parent, OldParent string
}

// Upload is one upload in progress at a provider. It is used by one goroutine at a
// time. Nothing is stored at the provider under the file's name before the last range
// was accepted.
type Upload interface {
	// Granularity is the number of bytes every range but the last must be a multiple of.
	Granularity() int64
	// Send passes on the next range: length bytes read from body, which belong at
	// offset. The range that reaches the announced size is the last one; the provider
	// answers it with the stored file, which is returned as the receipt (id, size,
	// version, revision, checksum and time filled). For every other range the result is
	// nil.
	//
	// An error of kind KindExists, KindStale, KindFull, KindForbidden, KindDailyLimit or
	// KindNotFound (the session is gone) means the provider refused and stored nothing.
	// Any other error leaves open what the provider has: the caller asks State. Send
	// never sends a range a second time by itself.
	Send(ctx context.Context, offset int64, body io.Reader, length int64) (*Item, error)
	// State asks the provider where the upload stands: the number of bytes it has, or,
	// when the upload is complete, the stored file. A session the provider no longer
	// knows is KindNotFound.
	State(ctx context.Context) (received int64, done *Item, err error)
	// Cancel ends the upload at the provider. A session that is already gone is no error.
	Cancel(ctx context.Context) error
}
