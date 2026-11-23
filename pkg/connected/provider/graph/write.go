package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

const (
	conflictBehavior  = "@microsoft.graph.conflictBehavior"
	uploadGranularity = 320 * 1024
	maxWriteJSON      = 1024 * 1024
)

var _ provider.Writer = (*client)(nil)
var _ provider.Upload = (*upload)(nil)

func (c *client) CreateFolder(ctx context.Context, parentID, name string) (*provider.Item, error) {
	in := map[string]any{"name": name, "folder": struct{}{}, conflictBehavior: "fail"}
	address := c.itemURL(c.driveID, parentID) + "/children" + selectQuery(itemFields)
	return c.writeItem(ctx, http.MethodPost, address, in)
}

func (c *client) CreateEmpty(ctx context.Context, parentID, name string) (*provider.Item, error) {
	address := c.itemURL(c.driveID, parentID) + ":/" + segment(name) + ":/content" +
		selectQuery(itemFields) + "&" + conflictBehavior + "=fail"
	return c.writeItem(ctx, http.MethodPut, address, nil)
}

func (c *client) Update(ctx context.Context, id string, change provider.Change) (*provider.Item, error) {
	in := make(map[string]any)
	if change.Name != "" {
		in["name"] = change.Name
	}
	if change.Parent != "" {
		in["parentReference"] = map[string]string{"id": change.Parent}
	}
	return c.writeItem(ctx, http.MethodPatch, c.itemURL(c.driveID, id)+selectQuery(itemFields), in)
}

func (c *client) Trash(ctx context.Context, id string) error {
	return c.write(ctx, http.MethodDelete, c.itemURL(c.driveID, id), nil, nil, nil)
}

func (c *client) writeItem(ctx context.Context, method, address string, in any) (*provider.Item, error) {
	var entry driveItem
	if err := c.write(ctx, method, address, nil, in, &entry); err != nil {
		return nil, err
	}
	return writeResult(&entry)
}

// A failed write can leave the outcome open, so use Stream instead of the retrying Caller.Do.
func (c *client) write(ctx context.Context, method, address string, header http.Header, in, out any) error {
	if header == nil {
		header = make(http.Header)
	}
	header.Set("Accept", "application/json")
	var body []byte
	if in != nil {
		var err error
		body, err = json.Marshal(in)
		if err != nil {
			return &provider.Error{Kind: provider.KindOther}
		}
		header.Set("Content-Type", "application/json")
	}
	res, err := c.caller.Stream(ctx, method, address, header, bytes.NewReader(body), int64(len(body)), true)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	status := res.StatusCode
	switch {
	case status >= 400:
		failure := c.caller.Failure(res).(*provider.Error)
		switch {
		case method == http.MethodPut && status == http.StatusConflict:
			failure.Kind = provider.KindExists
		case method == http.MethodDelete && status == http.StatusNotFound:
			failure.Kind = provider.KindNotFound
		}
		return failure
	case out == nil && status == http.StatusNoContent:
		return nil
	case out != nil && (status == http.StatusOK || status == http.StatusCreated):
		return decodeWrite(res, out)
	default:
		return &provider.Error{Kind: provider.KindOther, Status: status}
	}
}

func decodeWrite(res *http.Response, out any) error {
	if err := json.NewDecoder(io.LimitReader(res.Body, maxWriteJSON)).Decode(out); err != nil {
		return &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
	}
	return nil
}

func writeResult(entry *driveItem) (*provider.Item, error) {
	if item := entry.asItem(); item != nil && item.ID != "" {
		return item, nil
	}
	return nil, &provider.Error{Kind: provider.KindOther}
}

func (c *client) NewUpload(ctx context.Context, parentID, name string, size int64) (provider.Upload, error) {
	address := c.itemURL(c.driveID, parentID) + ":/" + segment(name) + ":/createUploadSession"
	return c.startUpload(ctx, address, "fail", "", size)
}

func (c *client) ReplaceUpload(ctx context.Context, item *provider.Item, size int64) (provider.Upload, error) {
	if item == nil || item.Link != nil {
		return nil, &provider.Error{Kind: provider.KindForbidden}
	}
	address := c.itemURL(c.driveID, item.ID) + "/createUploadSession"
	return c.startUpload(ctx, address, "replace", item.ETag, size)
}

func (c *client) startUpload(ctx context.Context, address, behavior, etag string, size int64) (provider.Upload, error) {
	if size <= 0 {
		return nil, &provider.Error{Kind: provider.KindOther}
	}
	header := make(http.Header)
	if etag != "" {
		header.Set("If-Match", etag)
	}
	in := map[string]any{"item": map[string]string{conflictBehavior: behavior}}
	var result struct {
		URL string `json:"uploadUrl"`
	}
	if err := c.write(ctx, http.MethodPost, address, header, in, &result); err != nil {
		return nil, err
	}
	u, err := url.Parse(result.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" ||
		(u.Scheme != "https" && !(c.caller.AllowHTTP && u.Scheme == "http")) {
		return nil, &provider.Error{Kind: provider.KindUnsafe}
	}
	return &upload{caller: &c.caller, address: result.URL, size: size}, nil
}

type upload struct {
	caller  *provider.Caller
	address string
	size    int64
}

// Upload addresses are credentials, so omit them when a handle is formatted.
func (*upload) String() string     { return "Graph upload session" }
func (u *upload) GoString() string { return u.String() }

func (u *upload) Granularity() int64 { return uploadGranularity }

func (u *upload) Send(ctx context.Context, offset int64, body io.Reader, length int64) (*provider.Item, error) {
	if offset < 0 || length <= 0 || length > u.size-offset || body == nil {
		return nil, &provider.Error{Kind: provider.KindOther}
	}
	end := offset + length
	header := http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", offset, end-1, u.size)}}
	// Hide any rewind method so the transport receives a streamed body.
	res, err := u.caller.Stream(ctx, http.MethodPut, u.address, header, struct{ io.Reader }{body}, length, false)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusAccepted:
		if end < u.size {
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxWriteJSON))
			return nil, nil
		}
	case http.StatusOK, http.StatusCreated:
		if end == u.size {
			var entry driveItem
			if err := decodeWrite(res, &entry); err != nil {
				return nil, err
			}
			if entry.File != nil {
				return writeResult(&entry)
			}
		}
	case http.StatusConflict:
		failure := u.caller.Failure(res).(*provider.Error)
		if failure.Code == "nameAlreadyExists" {
			return nil, failure
		}
	case http.StatusNotFound, http.StatusPreconditionFailed, http.StatusInsufficientStorage:
		return nil, provider.ByStatus(res.StatusCode, res.Header, nil)
	}
	// Even 416 cannot say whether a previous request committed the content.
	return nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
}

func (u *upload) State(ctx context.Context) (int64, *provider.Item, error) {
	res, err := u.caller.Stream(ctx, http.MethodGet, u.address, nil, nil, 0, false)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return 0, nil, provider.ByStatus(res.StatusCode, res.Header, nil)
	}
	if res.StatusCode != http.StatusOK {
		return 0, nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
	}
	var state struct {
		Ranges []string `json:"nextExpectedRanges"`
	}
	if err := decodeWrite(res, &state); err != nil {
		return 0, nil, err
	}
	if len(state.Ranges) != 0 {
		start, _, found := strings.Cut(state.Ranges[0], "-")
		offset, err := strconv.ParseInt(start, 10, 64)
		if found && err == nil && offset >= 0 && offset < u.size {
			return offset, nil, nil
		}
	}
	return 0, nil, &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
}

func (u *upload) Cancel(ctx context.Context) error {
	res, err := u.caller.Stream(ctx, http.MethodDelete, u.address, nil, nil, 0, false)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusNotFound {
		return nil
	}
	return &provider.Error{Kind: provider.KindOther, Status: res.StatusCode}
}
