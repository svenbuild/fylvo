// Package drive reads a Google Drive through the Drive API v3.
//
// It speaks plain HTTPS through provider.Caller, which adds the access token, renews
// it on a 401, obeys rate limits and follows content redirects without the token.
package drive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

const (
	defaultBaseURL = "https://www.googleapis.com"

	googleType   = "application/vnd.google-apps."
	folderType   = googleType + "folder"
	shortcutType = googleType + "shortcut"

	// pageSize is the largest page Drive hands out of a listing.
	pageSize = 1000
	// maxNamedPages bounds the pages Named reads. A name filter that needs more than
	// this many pages means Drive is not filtering, and the answer would not be complete.
	maxNamedPages = 10

	itemFields = "id,name,mimeType,size,modifiedTime,md5Checksum,headRevisionId,version,trashed," +
		"webViewLink,resourceKey,capabilities(canReadRevisions)," +
		"shortcutDetails(targetId,targetMimeType,targetResourceKey)"

	// resourceKeyHeader carries the resource key that some shared items need to be opened.
	resourceKeyHeader = "X-Goog-Drive-Resource-Keys"
)

// natives are the Google documents that are shown, by their kind in Item.Native. Every
// other Google type (forms, sites, maps, scripts, ...) has nothing to show and is left out.
var natives = map[string]string{
	googleType + "document":     "gdoc",
	googleType + "spreadsheet":  "gsheet",
	googleType + "presentation": "gslides",
	googleType + "drawing":      "gdraw",
}

// exports are the formats each kind of native document can be downloaded in.
var exports = map[string]map[string]string{
	"gdoc": {
		"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"pdf":  "application/pdf",
	},
	"gsheet": {
		"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"pdf":  "application/pdf",
	},
	"gslides": {
		"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"pdf":  "application/pdf",
	},
	"gdraw": {
		"svg": "image/svg+xml",
		"pdf": "application/pdf",
	},
}

// Options configure a client for one connection.
type Options struct {
	// HTTP comes from provider.NewHTTPClient() in production.
	HTTP *http.Client
	Auth provider.Auth
	// BaseURL replaces https://www.googleapis.com for tests. Plain http is then allowed.
	BaseURL string
}

type client struct {
	call provider.Caller
	// api is the address of the Drive API, ending in a slash.
	api string

	mu   sync.Mutex
	root string
	// keys are the resource keys some items need to be reached, by item id. They come
	// with the shortcuts that point to these items.
	keys map[string]string
}

// maxKeys bounds what is remembered of resource keys. Few items have one.
const maxKeys = 10000

// remember keeps the resource key a shortcut carries for its target.
func (c *client) remember(item *provider.Item) {
	if item.Link != nil {
		c.rememberKey(item.Link.TargetID, item.Link.ResourceKey)
	}
}

// rememberKey keeps the resource key of the item with the given id, if it has one.
func (c *client) rememberKey(id, key string) {
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keys == nil || len(c.keys) >= maxKeys {
		c.keys = map[string]string{}
	}
	c.keys[id] = key
}

// mapped maps an entry of Drive to an Item and keeps the resource keys it tells.
func (c *client) mapped(f *file) (provider.Item, bool) {
	item, ok := f.item()
	if ok {
		c.rememberKey(f.ID, f.ResourceKey)
		c.remember(&item)
	}
	return item, ok
}

// keyed marks a call about the item with the given id with its resource key, if it has one.
func (c *client) keyed(ctx context.Context, id string) context.Context {
	c.mu.Lock()
	key := c.keys[id]
	c.mu.Unlock()
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, resourceKeyContext{}, id+"/"+key)
}

// New returns the Drive client of one connection.
func New(o Options) provider.Provider {
	base, allowHTTP := defaultBaseURL, false
	if o.BaseURL != "" {
		base, allowHTTP = strings.TrimSuffix(o.BaseURL, "/"), true
	}
	httpClient := o.HTTP
	if u, err := url.Parse(base); err == nil {
		httpClient = withResourceKeys(o.HTTP, u.Host)
	}
	return &client{
		call: provider.Caller{HTTP: httpClient, Auth: o.Auth, Classify: classify, AllowHTTP: allowHTTP},
		api:  base + "/drive/v3/",
	}
}

func (c *client) CaseInsensitive() bool { return false }

func (c *client) Root(ctx context.Context) (string, error) {
	c.mu.Lock()
	root := c.root
	c.mu.Unlock()
	if root != "" {
		return root, nil
	}

	var answer struct {
		ID string `json:"id"`
	}
	if err := c.call.JSON(ctx, http.MethodGet, c.api+"files/root?fields=id", nil, &answer); err != nil {
		return "", err
	}
	if answer.ID == "" {
		return "", &provider.Error{Kind: provider.KindOther}
	}
	c.mu.Lock()
	c.root = answer.ID
	c.mu.Unlock()
	return answer.ID, nil
}

func (c *client) Children(ctx context.Context, folderID, page string) (*provider.Page, error) {
	list, err := c.list(c.keyed(ctx, folderID), parentQuery(folderID), page)
	if err != nil {
		return nil, err
	}
	// A page may come back empty with a token for the next one; that is not the end.
	res := &provider.Page{Items: make([]provider.Item, 0, len(list.Files)), Next: list.NextPageToken}
	for _, f := range list.Files {
		if item, ok := c.mapped(&f); ok {
			res.Items = append(res.Items, item)
		}
	}
	return res, nil
}

func (c *client) Named(ctx context.Context, folderID, name string) ([]provider.Item, error) {
	q := parentQuery(folderID) + " and name = '" + escape(name) + "'"
	var (
		res  []provider.Item
		page string
	)
	ctx = c.keyed(ctx, folderID)
	for n := 0; n < maxNamedPages; n++ {
		list, err := c.list(ctx, q, page)
		if err != nil {
			return nil, err
		}
		for _, f := range list.Files {
			// Drive's name filter may ignore case; only the exact name counts here.
			if f.Name != name {
				continue
			}
			if item, ok := c.mapped(&f); ok {
				res = append(res, item)
			}
		}
		if page = list.NextPageToken; page == "" {
			return res, nil
		}
	}
	return nil, &provider.Error{Kind: provider.KindIncomplete}
}

func (c *client) Get(ctx context.Context, id string) (*provider.Item, error) {
	var f file
	address := c.api + "files/" + url.PathEscape(id) + "?fields=" + url.QueryEscape(itemFields)
	if err := c.call.JSON(c.keyed(ctx, id), http.MethodGet, address, nil, &f); err != nil {
		return nil, err
	}
	item, ok := c.mapped(&f)
	if !ok {
		// Trashed, or a Google type that has nothing to show.
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	return &item, nil
}

func (c *client) Target(ctx context.Context, link *provider.Item) (*provider.Item, error) {
	if link.Link == nil {
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	c.remember(link)
	target, err := c.Get(ctx, link.Link.TargetID)
	if err != nil {
		return nil, err
	}
	if target.IsDir != link.IsDir || target.Link != nil || target.Native != link.Native {
		// Not what the shortcut was made for: nothing that can be read through it.
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	fresh := *link
	fresh.Size, fresh.ModTime, fresh.Hash = target.Size, target.ModTime, target.Hash
	fresh.Version, fresh.Revision = target.Version, target.Revision
	return &fresh, nil
}

func (c *client) Open(ctx context.Context, item *provider.Item, rangeHeader string) (*provider.Content, error) {
	if item.Native != "" || item.IsDir {
		return nil, &provider.Error{Kind: provider.KindForbidden}
	}

	id := item.ContentID()
	byRevision := item.Revision != ""
	address := c.api + "files/" + url.PathEscape(id) + "?alt=media"
	if byRevision {
		address = c.api + "files/" + url.PathEscape(id) + "/revisions/" + url.PathEscape(item.Revision) + "?alt=media"
	}
	c.remember(item)
	ctx = c.keyed(ctx, id)
	// Drive refuses every range of an empty file, and the whole of it is the answer
	// anyway. A shortcut from a listing does not tell the size of its target.
	if item.Size == 0 && (item.Link == nil || item.Version != "") {
		rangeHeader = ""
	}

	content, err := c.call.Download(ctx, address, rangeHeader)
	if err != nil && byRevision {
		// Drive answers 404, or 403 without a more telling reason, for a revision it no
		// longer serves. Rate limits and blocked files keep their own kind.
		var e *provider.Error
		if errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Status == http.StatusForbidden) &&
			(e.Kind == provider.KindNotFound || e.Kind == provider.KindForbidden) {
			return nil, &provider.Error{Kind: provider.KindStale, Status: e.Status, Code: e.Code}
		}
	}
	return content, err
}

func (c *client) Export(ctx context.Context, item *provider.Item, format string) (*provider.Content, error) {
	mimeType, ok := exports[item.Native][format]
	if !ok {
		return nil, &provider.Error{Kind: provider.KindForbidden}
	}
	// A shortcut to a document is exported by the document it points to.
	id := item.ContentID()
	c.remember(item)
	address := c.api + "files/" + url.PathEscape(id) + "/export?mimeType=" + url.QueryEscape(mimeType)
	return c.call.Download(c.keyed(ctx, id), address, "")
}

func (c *client) About(ctx context.Context) (*provider.Usage, error) {
	var answer struct {
		StorageQuota struct {
			// Both arrive as strings. Without a limit the storage has none.
			Limit int64 `json:"limit,string"`
			Usage int64 `json:"usage,string"`
		} `json:"storageQuota"`
	}
	address := c.api + "about?fields=" + url.QueryEscape("storageQuota(limit,usage)")
	if err := c.call.JSON(ctx, http.MethodGet, address, nil, &answer); err != nil {
		return nil, err
	}
	return &provider.Usage{Used: answer.StorageQuota.Usage, Total: answer.StorageQuota.Limit}, nil
}

// fileList is one page of a listing as Drive sends it.
type fileList struct {
	Files            []file `json:"files"`
	NextPageToken    string `json:"nextPageToken"`
	IncompleteSearch bool   `json:"incompleteSearch"`
}

func (c *client) list(ctx context.Context, q, page string) (*fileList, error) {
	v := url.Values{
		"q":        {q},
		"spaces":   {"drive"},
		"corpora":  {"user"},
		"pageSize": {"1000"},
		"fields":   {"nextPageToken,incompleteSearch,files(" + itemFields + ")"},
	}
	if page != "" {
		v.Set("pageToken", page)
	}
	var list fileList
	if err := c.call.JSON(ctx, http.MethodGet, c.api+"files?"+v.Encode(), nil, &list); err != nil {
		return nil, err
	}
	if list.IncompleteSearch {
		return nil, &provider.Error{Kind: provider.KindIncomplete}
	}
	return &list, nil
}

func parentQuery(folderID string) string {
	return "'" + escape(folderID) + "' in parents and trashed = false"
}

// escape makes s safe inside a quoted string of a Drive query.
func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

// file is an entry as Drive sends it, with the fields in itemFields.
type file struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	MimeType       string `json:"mimeType"`
	Size           int64  `json:"size,string"`
	ModifiedTime   string `json:"modifiedTime"`
	MD5Checksum    string `json:"md5Checksum"`
	HeadRevisionID string `json:"headRevisionId"`
	Trashed        bool   `json:"trashed"`
	WebViewLink    string `json:"webViewLink"`
	ResourceKey    string `json:"resourceKey"`
	Capabilities   *struct {
		CanReadRevisions bool `json:"canReadRevisions"`
	} `json:"capabilities"`
	Shortcut *struct {
		TargetID          string `json:"targetId"`
		TargetMimeType    string `json:"targetMimeType"`
		TargetResourceKey string `json:"targetResourceKey"`
	} `json:"shortcutDetails"`
}

// item maps a Drive entry to an Item. ok is false for an entry that is not shown:
// a trashed one, a Google type without content, or a shortcut to such a type.
func (f *file) item() (item provider.Item, ok bool) {
	if f.Trashed || f.ID == "" {
		return item, false
	}
	modTime, _ := time.Parse(time.RFC3339Nano, f.ModifiedTime)
	item = provider.Item{ID: f.ID, Name: f.Name, ModTime: modTime, WebURL: f.WebViewLink}

	switch {
	case f.MimeType == folderType:
		item.IsDir = true
	case f.MimeType == shortcutType:
		// Only what the shortcut points to can be opened. The target is not fetched for
		// a listing, so size and version stay empty.
		if f.Shortcut == nil || f.Shortcut.TargetID == "" {
			return item, false
		}
		target := f.Shortcut.TargetMimeType
		if strings.HasPrefix(target, googleType) && target != folderType {
			// A shortcut to a document that exists only at Google shows as such a
			// document; one to a Google type without content is not shown.
			native, known := natives[target]
			if !known {
				return item, false
			}
			item.Native = native
		}
		item.IsDir = target == folderType
		item.Link = &provider.Link{TargetID: f.Shortcut.TargetID, ResourceKey: f.Shortcut.TargetResourceKey}
	case strings.HasPrefix(f.MimeType, googleType):
		native, known := natives[f.MimeType]
		if !known {
			return item, false
		}
		// A native document has no bytes; every edit changes its time.
		item.Native = native
		item.Version = timeVersion(f.ModifiedTime)
	default:
		item.Size = f.Size
		item.Hash = f.MD5Checksum
		// Reading a revision by its id is for accounts that may see the revisions of
		// the file. A file shared for viewing only is read as it is now, after its
		// version was compared.
		if f.Capabilities == nil || f.Capabilities.CanReadRevisions {
			item.Revision = f.HeadRevisionID
		}
		switch {
		case f.HeadRevisionID != "":
			item.Version = "r:" + f.HeadRevisionID
		case f.MD5Checksum != "":
			item.Version = "h:" + f.MD5Checksum + ":" + strconv.FormatInt(f.Size, 10)
		default:
			item.Version = timeVersion(f.ModifiedTime)
		}
	}
	return item, true
}

// timeVersion is the version of content that has nothing but a time to tell it by.
// Without the time there is no version: nothing can be tied to such content.
func timeVersion(modifiedTime string) string {
	if modifiedTime == "" {
		return ""
	}
	return "t:" + modifiedTime
}

// reasons maps the reason of a Drive error to its kind. A reason not listed here is
// classified by the status code.
var reasons = map[string]provider.Kind{
	"notFound":                    provider.KindNotFound,
	"rateLimitExceeded":           provider.KindBusy,
	"userRateLimitExceeded":       provider.KindBusy,
	"backendError":                provider.KindBusy,
	"dailyLimitExceeded":          provider.KindDailyLimit,
	"downloadQuotaExceeded":       provider.KindDailyLimit,
	"sharingRateLimitExceeded":    provider.KindDailyLimit,
	"storageQuotaExceeded":        provider.KindFull,
	"cannotDownloadAbusiveFile":   provider.KindFlagged,
	"exportSizeLimitExceeded":     provider.KindExportTooLarge,
	"insufficientFilePermissions": provider.KindForbidden,
	"appNotAuthorizedToFile":      provider.KindForbidden,
	"forbidden":                   provider.KindForbidden,
	"domainPolicy":                provider.KindForbidden,
}

// safeCode is what a reason must look like to be kept as Error.Code: a short word.
// Anything else could be text of the provider and is dropped.
var safeCode = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// classify reads Drive's error answer: {"error":{"code":403,"errors":[{"reason":"..."}]}}.
// Only the first reason is used; the message is never read.
func classify(status int, header http.Header, body []byte) *provider.Error {
	e := provider.ByStatus(status, header, nil)
	var answer struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &answer) != nil || len(answer.Error.Errors) == 0 {
		return e
	}
	reason := answer.Error.Errors[0].Reason
	if !safeCode.MatchString(reason) {
		return e
	}
	e.Code = reason
	if kind, ok := reasons[reason]; ok {
		e.Kind = kind
	}
	return e
}
