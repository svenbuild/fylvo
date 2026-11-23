package drive

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func picture(t *testing.T, side int) []byte {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, image.NewRGBA(image.Rect(0, 0, side, side))))
	return out.Bytes()
}

func named(t *testing.T, d provider.Provider, folder, name string) *provider.Item {
	t.Helper()
	items, err := d.Named(context.Background(), folder, name)
	require.NoError(t, err)
	require.Len(t, items, 1)
	return &items[0]
}

// A listing tells which entries Drive has a preview of, and nothing more: the link to
// one is asked for only when the preview is wanted.
func TestThumbnail(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	thumbs := d.(provider.Thumbnailer)
	photo := s.AddFile("", "photo.png", picture(t, 8))
	s.AddFile("", "notes.txt", []byte("notes"))
	doc := s.AddNative("", "Budget", "gsheet")
	s.SetThumbnail(doc, picture(t, 4))
	folder := s.AddFolder("", "Photos")
	s.AddShortcut("", "to photo", photo)

	page, err := d.Children(ctx, s.RootID(), "")
	require.NoError(t, err)
	previews := map[string]bool{}
	for _, item := range page.Items {
		previews[item.Name] = item.Preview
	}
	assert.Equal(t, map[string]bool{"photo.png": true, "notes.txt": false, "Budget": true, "Photos": false, "to photo": false}, previews)
	assert.Zero(t, s.Calls(drivetest.CallThumbnail))

	item := named(t, d, s.RootID(), "photo.png")
	content, err := thumbs.Thumbnail(ctx, item, 400)
	require.NoError(t, err)
	assert.Equal(t, string(picture(t, 8)), read(t, content))
	// The size is asked for in the link, in place of Drive's own.
	assert.Equal(t, []string{"s400"}, s.ThumbnailSizes())

	// A native document has one too.
	content, err = thumbs.Thumbnail(ctx, named(t, d, s.RootID(), "Budget"), 320)
	require.NoError(t, err)
	assert.Equal(t, string(picture(t, 4)), read(t, content))
	assert.Equal(t, []string{"s400", "s320"}, s.ThumbnailSizes())

	// None: a file Drive has no picture of, a folder, a shortcut, a file that is gone.
	for _, name := range []string{"notes.txt", "Photos", "to photo"} {
		_, err = thumbs.Thumbnail(ctx, named(t, d, s.RootID(), name), 400)
		assert.Equal(t, provider.KindNotFound, provider.KindOf(err), name)
	}
	_ = folder
	// The preview Drive had when the listing was read is gone since.
	s.SetThumbnail(photo, nil)
	_, err = thumbs.Thumbnail(ctx, item, 400)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	assert.Len(t, s.ThumbnailSizes(), 2, "nothing was fetched for any of these")

	// The content changed since the row was read: the preview would be of another
	// state than the one asked for.
	s.SetThumbnail(photo, picture(t, 8))
	s.SetContent(photo, picture(t, 16))
	_, err = thumbs.Thumbnail(ctx, item, 400)
	assert.Equal(t, provider.KindStale, provider.KindOf(err))
	s.Trash(photo)
	_, err = thumbs.Thumbnail(ctx, item, 400)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	assert.Len(t, s.ThumbnailSizes(), 2)
}

// The access token goes to the API and to the host of the previews, and nowhere else:
// not after a redirect, and not to a link that leads to another host.
func TestThumbnailKeepsTheTokenAtHome(t *testing.T) {
	s, d, auth := setup(t)
	ctx := context.Background()
	thumbs := d.(provider.Thumbnailer)
	s.AddFile("", "photo.png", picture(t, 8))
	item := named(t, d, s.RootID(), "photo.png")

	// The host of the previews sends the request on: the token stays behind.
	s.RedirectThumbnails(true)
	content, err := thumbs.Thumbnail(ctx, item, 400)
	require.NoError(t, err)
	assert.Equal(t, string(picture(t, 8)), read(t, content))
	assert.Equal(t, []string{""}, s.ContentTokens(), "the content host got no token")
	s.RedirectThumbnails(false)

	// A link to anywhere else is not followed at all. The other server would have
	// answered; it is never asked.
	asked := 0
	other := newOtherHost(t, &asked)
	for _, elsewhere := range []string{other, "https://lh3.example.com", "https://lh3.googleusercontent.com.example.com", "http://lh3.googleusercontent.com"} {
		s.ThumbnailsAt(elsewhere)
		before := s.Calls(drivetest.CallThumbnail)
		_, err = thumbs.Thumbnail(ctx, item, 400)
		assert.Equal(t, provider.KindUnsafe, provider.KindOf(err), elsewhere)
		assert.Equal(t, before, s.Calls(drivetest.CallThumbnail), elsewhere)
	}
	assert.Zero(t, asked)
	s.ThumbnailsAt("")

	// A refusal at the host of the previews says something about that picture. No new
	// token is asked for on its account, and the connection is not doubted.
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		s.RefuseThumbnails(status)
		_, err = thumbs.Thumbnail(ctx, item, 400)
		assert.Equal(t, provider.KindForbidden, provider.KindOf(err), status)
	}
	s.RefuseThumbnails(http.StatusNotFound)
	_, err = thumbs.Thumbnail(ctx, item, 400)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	s.RefuseThumbnails(http.StatusTooManyRequests)
	_, err = thumbs.Thumbnail(ctx, item, 400)
	assert.Equal(t, provider.KindBusy, provider.KindOf(err))
	assert.Zero(t, auth.Renewals())
	s.RefuseThumbnails(0)
	// The same for a refusal by the host the image host sends the request on to.
	s.RedirectThumbnails(true)
	s.RefuseContent(http.StatusUnauthorized)
	_, err = thumbs.Thumbnail(ctx, item, 400)
	assert.Equal(t, provider.KindForbidden, provider.KindOf(err))
	assert.Zero(t, auth.Renewals())
	s.RefuseContent(0)
	s.RedirectThumbnails(false)
	_, err = thumbs.Thumbnail(ctx, item, 400)
	require.NoError(t, err)
}

func newOtherHost(t *testing.T, asked *int) string {
	t.Helper()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*asked++
		_, _ = w.Write(picture(t, 2))
	}))
	t.Cleanup(other.Close)
	return other.URL
}

// The hosts a preview link of the real Drive may name are fixed names. Everything else
// is refused before a request is made.
func TestThumbnailHostsOfDrive(t *testing.T) {
	c := New(Options{HTTP: http.DefaultClient, Auth: &drivetest.Auth{}}).(*client)
	require.Empty(t, c.thumbnailHost, "the real Drive has no host of its own choosing")

	for link, want := range map[string]string{
		"https://lh3.googleusercontent.com/drive-storage/AbC_dEf=s220": "https://lh3.googleusercontent.com/drive-storage/AbC_dEf=s400",
		"https://lh6.googleusercontent.com/AbC=s1600":                  "https://lh6.googleusercontent.com/AbC=s400",
		// Without a size of its own the link gets ours.
		"https://lh3.googleusercontent.com/drive-storage/AbC":          "https://lh3.googleusercontent.com/drive-storage/AbC=s400",
		"https://lh3.googleusercontent.com/drive-storage/AbC=w100-h50": "https://lh3.googleusercontent.com/drive-storage/AbC=w100-h50=s400",
		// A link of another form is asked for as it is.
		"https://lh3.googleusercontent.com/thumb?id=AbC&sz=s220": "https://lh3.googleusercontent.com/thumb?id=AbC&sz=s220",
		"https://lh3.googleusercontent.com/a%20b/AbC=s220":       "https://lh3.googleusercontent.com/a%20b/AbC=s400",
	} {
		got, ok := c.thumbnailAddress(link, 400)
		assert.True(t, ok, link)
		assert.Equal(t, want, got)
		// What is asked for lies on the host that was checked.
		before, err := url.Parse(link)
		require.NoError(t, err)
		after, err := url.Parse(got)
		require.NoError(t, err)
		assert.Equal(t, before.Host, after.Host, link)
		assert.Equal(t, "https", after.Scheme, link)
	}
	for _, link := range []string{
		"",
		// A link that names a host and no picture: the size must never become part
		// of the host.
		"https://lh3.googleusercontent.com",
		"https://lh3.googleusercontent.com/",
		"https://lh3.googleusercontent.com?x=1",
		"https:lh3.googleusercontent.com/AbC=s220",
		"http://lh3.googleusercontent.com/AbC=s220",
		"https://lh7.googleusercontent.com/AbC=s220",
		"https://lh3.googleusercontent.com.example.com/AbC=s220",
		"https://example.com/lh3.googleusercontent.com/AbC=s220",
		"https://lh3.googleusercontent.com:8443/AbC=s220",
		"https://user@lh3.googleusercontent.com/AbC=s220",
		"https://LH3.googleusercontent.com.evil.example/AbC=s220",
		"https://www.googleapis.com/drive/v3/files/x?alt=media",
		"https://docs.google.com/feeds/vt?id=x",
		"https://127.0.0.1/AbC=s220",
		"//lh3.googleusercontent.com/AbC=s220",
		"https://lh3.googleusercontent.com/AbC=s220#frag",
		"lh3.googleusercontent.com/AbC=s220",
	} {
		_, ok := c.thumbnailAddress(link, 400)
		assert.False(t, ok, link)
	}
}
