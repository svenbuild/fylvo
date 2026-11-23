package drive

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

// Previews: Drive makes them itself and hands out a link to one with the entry.
//
// The link is short-lived and lies on Google's image host, not on the API host, and for
// a file that is not public it has to be asked for with the access token. This is the
// one place where the token of a connection goes to another host than the API. The
// hosts are the names below and no others; a link to anywhere else is not followed.

// thumbnailFields are asked for one entry when its preview is wanted. The link is never
// asked for in a listing.
const thumbnailFields = itemFields + ",thumbnailLink"

// thumbnailHosts are the hosts a preview link of Drive may name.
var thumbnailHosts = map[string]bool{
	"lh3.googleusercontent.com": true,
	"lh4.googleusercontent.com": true,
	"lh5.googleusercontent.com": true,
	"lh6.googleusercontent.com": true,
}

// sizeSuffix is how a preview link of Drive names the size of its picture, at the end
// of its path: "=s220".
var sizeSuffix = regexp.MustCompile(`=s[0-9]+$`)

// thumbnailHostOK tells whether an address leads to a host previews are fetched from.
func (c *client) thumbnailHostOK(u *url.URL) bool {
	if u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if c.thumbnailHost != "" {
		// A simulated Drive serves its previews itself.
		return u.Host == c.thumbnailHost
	}
	return u.Scheme == "https" && thumbnailHosts[u.Host]
}

// thumbnailAddress is the address of the preview of the given size behind a link Drive
// handed out. ok is false for a link that does not lead to one of the hosts for
// previews over https, or that names no picture: nothing is sent there. The size goes
// into the path of the link, and the address that comes of it is checked like the link.
func (c *client) thumbnailAddress(link string, size int) (address string, ok bool) {
	u, err := url.Parse(link)
	if err != nil || !c.thumbnailHostOK(u) || len(u.EscapedPath()) < 2 {
		return "", false
	}
	if u.RawQuery == "" {
		// A link of another form is used as it is.
		path := u.EscapedPath()
		sized := "=s" + strconv.Itoa(size)
		if sizeSuffix.MatchString(path) {
			path = sizeSuffix.ReplaceAllString(path, sized)
		} else {
			path += sized
		}
		link = u.Scheme + "://" + u.Host + path
	}
	final, err := url.Parse(link)
	if err != nil || !c.thumbnailHostOK(final) || final.Host != u.Host || final.Scheme != u.Scheme {
		return "", false
	}
	return link, true
}

// Thumbnail starts a download of the preview Drive has of item, which fits into a
// square of size pixels. KindNotFound when Drive has none, KindStale when the content
// changed since item was read.
func (c *client) Thumbnail(ctx context.Context, item *provider.Item, size int) (*provider.Content, error) {
	if item.IsDir || item.Link != nil {
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	c.Known(item)
	var f struct {
		file
		ThumbnailLink string `json:"thumbnailLink"`
	}
	address := c.api + "files/" + url.PathEscape(item.ID) + "?fields=" + url.QueryEscape(thumbnailFields)
	if err := c.call.JSON(c.keyed(ctx, item.ID), http.MethodGet, address, nil, &f); err != nil {
		return nil, err
	}
	current, ok := c.mapped(&f.file)
	if !ok {
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	if current.Version != item.Version {
		return nil, &provider.Error{Kind: provider.KindStale}
	}
	if !current.Preview || f.ThumbnailLink == "" {
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	picture, ok := c.thumbnailAddress(f.ThumbnailLink, size)
	if !ok {
		return nil, &provider.Error{Kind: provider.KindUnsafe}
	}
	// With the token, to the host checked above. Should that host redirect, Download
	// follows without the token. A refusal there says something about this picture and
	// never about the connection: no new token is asked for on its account.
	pictures := c.call
	pictures.ItemOnly = true
	return pictures.Download(ctx, picture, "")
}
