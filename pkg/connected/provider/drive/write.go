package drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

const (
	// granularity is what every range of an upload but the last must be a multiple of.
	granularity = 256 * 1024
	// maxReceipt bounds an answer that carries one file.
	maxReceipt = 1024 * 1024
	// writeTimeout bounds a call that changes something or opens an upload, from its
	// start to the end of the answer.
	writeTimeout = 2 * time.Minute
)

var _ provider.Writer = (*client)(nil)

// fileFields asks Drive for the fields of a file that item() maps.
var fileFields = "fields=" + url.QueryEscape(itemFields)

func (c *client) CreateFolder(ctx context.Context, parentID, name string) (*provider.Item, error) {
	return c.create(ctx, parentID, map[string]any{"name": name, "mimeType": folderType, "parents": []string{parentID}})
}

func (c *client) CreateEmpty(ctx context.Context, parentID, name string) (*provider.Item, error) {
	item, err := c.create(ctx, parentID, map[string]any{"name": name, "parents": []string{parentID}})
	if err == nil && (item.IsDir || item.Native != "" || item.Link != nil) {
		// Not the answer for a plain file: what was made is open.
		return nil, &provider.Error{Kind: provider.KindOpen}
	}
	return item, err
}

// create adds an entry to a folder. Drive allows several entries with one name in a
// folder, so a taken name is no error here: the package above checks names first.
func (c *client) create(ctx context.Context, parentID string, meta map[string]any) (*provider.Item, error) {
	var f file
	if err := c.change(c.keyed(ctx, parentID), http.MethodPost, c.api+"files?"+fileFields, meta, &f); err != nil {
		return nil, err
	}
	return c.changed(&f)
}

func (c *client) Update(ctx context.Context, id string, change provider.Change) (*provider.Item, error) {
	query := url.Values{"fields": {itemFields}}
	if change.Parent != "" && change.Parent != change.OldParent {
		if change.OldParent == "" {
			// An entry has one parent; Drive must be told which one it leaves.
			return nil, &provider.Error{Kind: provider.KindOther}
		}
		query.Set("addParents", change.Parent)
		query.Set("removeParents", change.OldParent)
	}
	meta := map[string]any{}
	if change.Name != "" {
		meta["name"] = change.Name
	}
	var f file
	address := c.api + "files/" + url.PathEscape(id) + "?" + query.Encode()
	if err := c.change(c.keyed(ctx, id), http.MethodPatch, address, meta, &f); err != nil {
		return nil, err
	}
	return c.changed(&f)
}

func (c *client) Trash(ctx context.Context, id string) error {
	// Drive answers 404 for an item that is gone. One that is in the trash already is
	// simply trashed again.
	address := c.api + "files/" + url.PathEscape(id)
	return c.change(c.keyed(ctx, id), http.MethodPatch, address, map[string]any{"trashed": true}, nil)
}

// changed maps the answer of a call that changed an entry.
func (c *client) changed(f *file) (*provider.Item, error) {
	item, ok := c.mapped(f)
	if !ok {
		// Drive said yes and sent nothing to go by: what it did is open.
		return nil, &provider.Error{Kind: provider.KindOther}
	}
	return &item, nil
}

// change sends a call that changes something, with in as its JSON body, and reads the
// answer into out. Unlike Caller.JSON on its own it does not send the call again after
// an answer that leaves open whether Drive carried it out (a 5xx; a broken connection
// is never repeated anyway). A clear "slow down" is waited for and repeated, since
// Drive did nothing then.
func (c *client) change(ctx context.Context, method, address string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	once := c.call
	once.Classify = classifyOnce
	err := once.JSON(ctx, method, address, in, out)
	var e *provider.Error
	if errors.As(err, &e) && e.Kind == provider.KindOther && (e.Status >= 500 || reasons[e.Code] == provider.KindBusy) {
		// Marked by classifyOnce so that it was not repeated.
		e.Kind = provider.KindBusy
	}
	return err
}

// classifyOnce is classify for calls that change something. Caller.Do repeats a call
// whose answer is of kind KindBusy, which is right only where Drive said to slow down.
// Every other busy answer is marked KindOther here, and change turns it back.
func classifyOnce(status int, header http.Header, body []byte) *provider.Error {
	e := classify(status, header, body)
	slowDown := status == http.StatusTooManyRequests || e.Code == "rateLimitExceeded" || e.Code == "userRateLimitExceeded"
	if e.Kind == provider.KindBusy && !slowDown {
		e.Kind = provider.KindOther
	}
	return e
}

func (c *client) NewUpload(ctx context.Context, parentID, name string, size int64) (provider.Upload, error) {
	meta, err := json.Marshal(map[string]any{"name": name, "parents": []string{parentID}})
	if err != nil {
		return nil, &provider.Error{Kind: provider.KindOther}
	}
	return c.startUpload(c.keyed(ctx, parentID), http.MethodPost, c.uploadAPI(), meta, size)
}

// ReplaceUpload opens an upload of new content for a file. Drive takes no condition on
// the state of the file here, so item.ETag is not used: the package above compares the
// version right before it sends the last range.
func (c *client) ReplaceUpload(ctx context.Context, item *provider.Item, size int64) (provider.Upload, error) {
	if item.IsDir || item.Native != "" {
		return nil, &provider.Error{Kind: provider.KindForbidden}
	}
	id := item.ContentID()
	c.Known(item)
	return c.startUpload(c.keyed(ctx, id), http.MethodPatch, c.uploadAPI()+"/"+url.PathEscape(id), nil, size)
}

// uploadAPI is the address resumable uploads of new files start at.
func (c *client) uploadAPI() string {
	return strings.TrimSuffix(c.api, "drive/v3/") + "upload/drive/v3/files"
}

// startUpload opens a resumable upload session. Drive answers with the address of the
// session in Location.
func (c *client) startUpload(ctx context.Context, method, address string, meta []byte, size int64) (provider.Upload, error) {
	if size <= 0 {
		// An empty file is made with CreateEmpty.
		return nil, &provider.Error{Kind: provider.KindOther}
	}
	// No type is named for the content: Drive then finds it out itself, and the file
	// shows and previews there as what it is.
	header := http.Header{"X-Upload-Content-Length": {strconv.FormatInt(size, 10)}}
	if meta != nil {
		header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	// Opening a session stores nothing, so Caller.Do may send it again after any failure.
	// It has a time limit to the end of the answer: the caller holds the lock for
	// changes of the account.
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	res, err := c.call.Do(ctx, method, address+"?uploadType=resumable&"+fileFields, header, meta)
	if err != nil {
		return nil, err
	}
	// Only the header matters. The body is not waited for.
	location := res.Header.Get("Location")
	res.Body.Close()
	if res.StatusCode != http.StatusOK || location == "" {
		return nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
	}
	if !c.atAPI(location) {
		// The access token goes with every request of the session: only to Drive itself.
		return nil, &provider.Error{Kind: provider.KindUnsafe}
	}
	return &upload{c: c, session: location, size: size}, nil
}

// atAPI tells whether an address is on the scheme and host of the API.
func (c *client) atAPI(address string) bool {
	u, err := url.Parse(address)
	api, apiErr := url.Parse(c.api)
	return err == nil && apiErr == nil && u.Scheme == api.Scheme && u.Host == api.Host
}

// upload is a resumable upload session at Drive.
type upload struct {
	c *client
	// session is the address of the session. It works like a key while the upload is
	// open, so it goes to Drive and nowhere else.
	session string
	size    int64
}

func (u *upload) Granularity() int64 { return granularity }

func (u *upload) Send(ctx context.Context, offset int64, body io.Reader, length int64) (*provider.Item, error) {
	end := offset + length
	if offset < 0 || length <= 0 || end > u.size {
		return nil, &provider.Error{Kind: provider.KindOther}
	}
	header := http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", offset, end-1, u.size)}}
	res, err := u.c.call.Stream(ctx, http.MethodPut, u.session, header, body, length, true)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusPermanentRedirect:
		// "Go on". Drive has what its Range tells; anything short of the end of this
		// range, or a last range that did not complete the file, is for State to sort out.
		if n, ok := received(res.Header); !ok || n != end || end == u.size {
			return nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
		}
		return nil, nil
	case res.StatusCode == http.StatusOK || res.StatusCode == http.StatusCreated:
		if end != u.size {
			// A stored file where only a part was sent: what Drive did is open, and
			// stays so.
			return nil, &provider.Error{Kind: provider.KindOpen, Status: res.StatusCode}
		}
		return receipt(res)
	case res.StatusCode >= 400:
		return nil, refusal(u.c.call.Failure(res))
	}
	return nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
}

// refusal keeps the kinds that tell Drive refused a range and stored nothing. Drive has
// no unique names and takes no conditions, so an answer that looks like one of those
// tells nothing certain: it leaves the outcome open, and the caller asks State.
func refusal(err error) error {
	var e *provider.Error
	if errors.As(err, &e) && (e.Kind == provider.KindExists || e.Kind == provider.KindStale) {
		e.Kind = provider.KindOther
	}
	return err
}

func (u *upload) State(ctx context.Context) (int64, *provider.Item, error) {
	header := http.Header{"Content-Range": {"bytes */" + strconv.FormatInt(u.size, 10)}}
	res, err := u.c.call.Stream(ctx, http.MethodPut, u.session, header, nil, 0, true)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusPermanentRedirect:
		n, ok := received(res.Header)
		if !ok || n > u.size {
			return 0, nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
		}
		return n, nil, nil
	case res.StatusCode == http.StatusOK || res.StatusCode == http.StatusCreated:
		item, err := receipt(res)
		if err != nil {
			return 0, nil, err
		}
		return item.Size, item, nil
	case res.StatusCode >= 400:
		return 0, nil, u.c.call.Failure(res)
	}
	return 0, nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
}

func (u *upload) Cancel(ctx context.Context) error {
	res, err := u.c.call.Stream(ctx, http.MethodDelete, u.session, nil, nil, 0, true)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	// Drive answers 499 to a cancelled session, 404 to one it no longer knows.
	switch {
	case res.StatusCode < 300 || res.StatusCode == 499 ||
		res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return nil
	case res.StatusCode >= 400:
		return u.c.call.Failure(res)
	}
	return &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
}

// received reads the Range header of Drive's "go on" answer, "bytes=0-<last>": the
// number of bytes Drive has. Without the header it has none.
func received(h http.Header) (int64, bool) {
	value := h.Get("Range")
	if value == "" {
		return 0, true
	}
	last, found := strings.CutPrefix(value, "bytes=0-")
	n, err := strconv.ParseInt(last, 10, 64)
	if !found || err != nil || n < 0 {
		return 0, false
	}
	return n + 1, true
}

// receipt reads the stored file from Drive's answer to the request that completed an
// upload, mapped as in a listing.
func receipt(res *http.Response) (*provider.Item, error) {
	var f file
	if err := json.NewDecoder(io.LimitReader(res.Body, maxReceipt)).Decode(&f); err != nil {
		return nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
	}
	item, ok := f.item()
	if !ok || item.IsDir {
		return nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
	}
	return &item, nil
}
