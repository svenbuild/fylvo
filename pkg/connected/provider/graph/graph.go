// Package graph reads and writes OneDrive through Microsoft Graph.
package graph

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

const (
	graphOrigin = "https://graph.microsoft.com"
	apiPath     = "/v1.0"
	itemFields  = "id,name,size,folder,file,package,deleted,specialFolder,fileSystemInfo,lastModifiedDateTime,cTag,eTag,parentReference,remoteItem,webUrl"
)

// Options configure a client for one connection.
type Options struct {
	HTTP *http.Client
	Auth provider.Auth
	// DriveID is the connection's own drive, stored when the account connected.
	DriveID string
	// BaseURL replaces the Graph origin and allows plain HTTP in tests.
	BaseURL string
}

type client struct {
	caller  provider.Caller
	baseURL string
	driveID string

	rootMu sync.Mutex
	rootID string
}

// New creates a OneDrive client.
func New(o Options) provider.Provider {
	baseURL := graphOrigin
	if o.BaseURL != "" {
		baseURL = strings.TrimRight(o.BaseURL, "/")
	}

	if o.HTTP == nil {
		if o.BaseURL == "" {
			o.HTTP = provider.NewHTTPClient()
		} else {
			o.HTTP = http.DefaultClient
		}
	}
	httpClient := *o.HTTP
	// Download follows redirects itself so that only the API gets the token.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client{
		caller: provider.Caller{
			HTTP:      &httpClient,
			Auth:      o.Auth,
			Classify:  classify,
			AllowHTTP: o.BaseURL != "",
		},
		baseURL: baseURL,
		driveID: o.DriveID,
	}
}

func segment(value string) string {
	switch value {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	default:
		// A colon in a Graph path separates the id from a relative name.
		return strings.ReplaceAll(url.PathEscape(value), ":", "%3A")
	}
}

func (c *client) driveURL(driveID string) string {
	return c.baseURL + apiPath + "/drives/" + segment(driveID)
}

func (c *client) itemURL(driveID, id string) string {
	return c.driveURL(driveID) + "/items/" + segment(id)
}

func selectQuery(fields string) string {
	return "?$select=" + url.QueryEscape(fields)
}

func (c *client) Root(ctx context.Context) (string, error) {
	c.rootMu.Lock()
	defer c.rootMu.Unlock()
	if c.rootID != "" {
		return c.rootID, nil
	}
	var root struct {
		ID string `json:"id"`
	}
	if err := c.caller.JSON(ctx, http.MethodGet, c.driveURL(c.driveID)+"/root"+selectQuery("id"), nil, &root); err != nil {
		return "", err
	}
	if root.ID == "" {
		return "", &provider.Error{Kind: provider.KindBusy}
	}
	c.rootID = root.ID
	return c.rootID, nil
}

// safePage checks the address before the caller puts the token on it.
func (c *client) safePage(address string) bool {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return false
	}
	next, err := url.Parse(address)
	if err != nil || next.Scheme != base.Scheme || !strings.EqualFold(next.Host, base.Host) ||
		next.User != nil || next.Fragment != "" || !strings.HasPrefix(next.EscapedPath(), apiPath+"/") ||
		strings.Contains(next.Path, "\\") {
		return false
	}
	for _, part := range strings.Split(next.Path, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (c *client) Children(ctx context.Context, folderID, page string) (*provider.Page, error) {
	address := page
	if page == "" {
		address = c.itemURL(c.driveID, folderID) + "/children" + selectQuery(itemFields) + "&$top=200"
	} else if !c.safePage(page) {
		return nil, &provider.Error{Kind: provider.KindUnsafe}
	}
	var result struct {
		Items []driveItem `json:"value"`
		Next  string      `json:"@odata.nextLink"`
	}
	if err := c.caller.JSON(ctx, http.MethodGet, address, nil, &result); err != nil {
		return nil, err
	}
	out := &provider.Page{Next: result.Next}
	for _, entry := range result.Items {
		if item := entry.asItem(); item != nil {
			out.Items = append(out.Items, *item)
		}
	}
	return out, nil
}

func (c *client) Named(ctx context.Context, folderID, name string) ([]provider.Item, error) {
	address := c.itemURL(c.driveID, folderID) + ":/" + segment(name) + selectQuery(itemFields)
	var entry driveItem
	if err := c.caller.JSON(ctx, http.MethodGet, address, nil, &entry); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && (failure.Status == http.StatusNotFound || failure.Kind == provider.KindNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if item := entry.asItem(); item != nil {
		return []provider.Item{*item}, nil
	}
	return nil, nil
}

func (c *client) Get(ctx context.Context, id string) (*provider.Item, error) {
	var entry driveItem
	if err := c.caller.JSON(ctx, http.MethodGet, c.itemURL(c.driveID, id)+selectQuery(itemFields), nil, &entry); err != nil {
		return nil, err
	}
	if item := entry.asItem(); item != nil {
		return item, nil
	}
	return nil, &provider.Error{Kind: provider.KindNotFound}
}

func (c *client) Target(ctx context.Context, link *provider.Item) (*provider.Item, error) {
	if link.Link == nil {
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	var entry driveItem
	address := c.itemURL(link.Link.TargetDrive, link.Link.TargetID) + selectQuery(itemFields)
	if err := c.caller.JSON(ctx, http.MethodGet, address, nil, &entry); err != nil {
		return nil, err
	}
	// The answer is the item itself, not a link to it.
	if entry.hidden() || entry.RemoteItem != nil || (entry.Folder != nil) != link.IsDir {
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	fresh := *link
	fresh.Size, fresh.Hash, fresh.Version = entry.Size, "", ""
	fresh.ModTime = entry.FileSystemInfo.LastModifiedDateTime
	if fresh.ModTime.IsZero() {
		fresh.ModTime = entry.LastModifiedDateTime
	}
	if entry.File != nil {
		fresh.Hash = entry.File.Hashes.QuickXorHash
		fresh.Version = entry.version()
	}
	return &fresh, nil
}

func (c *client) Open(ctx context.Context, item *provider.Item, rangeHeader string) (*provider.Content, error) {
	if item.IsDir {
		return nil, &provider.Error{Kind: provider.KindForbidden}
	}
	driveID := c.driveID
	if item.Link != nil {
		driveID = item.Link.TargetDrive
	}
	return c.caller.Download(ctx, c.itemURL(driveID, item.ContentID())+"/content", rangeHeader)
}

func (c *client) Export(context.Context, *provider.Item, string) (*provider.Content, error) {
	return nil, &provider.Error{Kind: provider.KindForbidden}
}

func (c *client) About(ctx context.Context) (*provider.Usage, error) {
	var result struct {
		Quota struct {
			Used  int64 `json:"used"`
			Total int64 `json:"total"`
		} `json:"quota"`
	}
	if err := c.caller.JSON(ctx, http.MethodGet, c.driveURL(c.driveID)+selectQuery("quota"), nil, &result); err != nil {
		return nil, err
	}
	return &provider.Usage{Used: result.Quota.Used, Total: result.Quota.Total}, nil
}

func (c *client) CaseInsensitive() bool { return true }
