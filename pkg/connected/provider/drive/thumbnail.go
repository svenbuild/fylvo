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

// sizeSuffix is how a preview link of Drive names the size of its picture: "=s220".
var sizeSuffix = regexp.MustCompile(`=s[0-9]+$`)

// thumbnailAddress is the address of the preview of the given size behind a link Drive
// handed out. ok is false for a link that does not lead to one of the hosts for
// previews over https: nothing is sent there.
func (c *client) thumbnailAddress(link string, size int) (address string, ok bool) {
	u, err := url.Parse(link)
	if err != nil || u.User != nil || u.Fragment != "" {
		return "", false
	}
	switch {
	case c.thumbnailHost != "":
		// A simulated Drive serves its previews itself.
		if u.Host != c.thumbnailHost {
			return "", false
		}
	case u.Scheme != "https" || !thumbnailHosts[u.Host]:
		return "", false
	}
	sized := "=s" + strconv.Itoa(size)
	if sizeSuffix.MatchString(link) {
		return sizeSuffix.ReplaceAllString(link, sized), true
	}
	return link + sized, true
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
