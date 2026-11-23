package drive

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupWriter(t *testing.T) (*drivetest.Server, provider.Provider, provider.Writer, *drivetest.Auth) {
	s, d, auth := setup(t)
	w, ok := d.(provider.Writer)
	require.True(t, ok)
	return s, d, w, auth
}

// pattern returns n bytes that differ from place to place.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7 % 251)
	}
	return b
}

func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// noAddress checks that err tells nothing of the address of an upload session.
func noAddress(t *testing.T, s *drivetest.Server, err error) {
	t.Helper()
	require.Error(t, err)
	for _, part := range []string{"upload_id", "session", strings.TrimPrefix(s.URL(), "http://")} {
		assert.NotContains(t, err.Error(), part)
	}
}

// send passes on one range of content, from offset to offset+length.
func send(t *testing.T, u provider.Upload, content []byte, offset, length int64) (*provider.Item, error) {
	t.Helper()
	return u.Send(context.Background(), offset, bytes.NewReader(content[offset:offset+length]), length)
}

func TestCreate(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	parent := s.AddFolder("", "Work")

	cases := []struct {
		name   string
		create func(ctx context.Context, parentID, name string) (*provider.Item, error)
		isDir  bool
	}{
		{"Photos", w.CreateFolder, true},
		{"notes.txt", w.CreateEmpty, false},
	}
	for _, tc := range cases {
		item, err := tc.create(ctx, parent, tc.name)
		require.NoError(t, err, tc.name)
		assert.NotEmpty(t, item.ID, tc.name)
		assert.Equal(t, tc.name, item.Name)
		assert.Equal(t, tc.isDir, item.IsDir, tc.name)
		assert.Zero(t, item.Size, tc.name)
		assert.False(t, item.ModTime.IsZero(), tc.name)
		got, err := d.Get(ctx, item.ID)
		require.NoError(t, err, tc.name)
		assert.Equal(t, item, got, tc.name)

		// Drive takes a taken name: a second entry with the same name.
		again, err := tc.create(ctx, parent, tc.name)
		require.NoError(t, err, tc.name)
		assert.NotEqual(t, item.ID, again.ID, tc.name)
		found, err := d.Named(ctx, parent, tc.name)
		require.NoError(t, err)
		assert.Len(t, found, 2, tc.name)
	}

	empty, err := w.CreateEmpty(ctx, parent, "empty.bin")
	require.NoError(t, err)
	assert.Equal(t, md5Hex(nil), empty.Hash)
	assert.NotEmpty(t, empty.Version)

	_, err = w.CreateFolder(ctx, "missing", "x")
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	assert.Equal(t, 6, s.Calls(drivetest.CallCreate))
}

func TestUpdate(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	from := s.AddFolder("", "From")
	to := s.AddFolder("", "To")

	cases := []struct {
		label  string
		change func(id string) provider.Change
		name   string
		parent string
	}{
		{"rename", func(string) provider.Change { return provider.Change{Name: "b.txt"} }, "b.txt", from},
		{"move", func(string) provider.Change { return provider.Change{Parent: to, OldParent: from} }, "a.txt", to},
		{"both", func(string) provider.Change { return provider.Change{Name: "c.txt", Parent: to, OldParent: from} }, "c.txt", to},
		{"same parent", func(string) provider.Change { return provider.Change{Name: "d.txt", Parent: from, OldParent: from} }, "d.txt", from},
	}
	for _, tc := range cases {
		id := s.AddFile(from, "a.txt", []byte("abc"))
		before, err := d.Get(ctx, id)
		require.NoError(t, err)
		item, err := w.Update(ctx, id, tc.change(id))
		require.NoError(t, err, tc.label)
		assert.Equal(t, id, item.ID, tc.label)
		assert.Equal(t, tc.name, item.Name, tc.label)
		assert.Equal(t, before.Version, item.Version, tc.label+": the content stays")

		found, err := d.Named(ctx, tc.parent, tc.name)
		require.NoError(t, err)
		require.Len(t, found, 1, tc.label)
		assert.Equal(t, id, found[0].ID, tc.label)
		s.Delete(id)
	}

	// Drive must know the parent an entry leaves; nothing is sent without it.
	id := s.AddFile(from, "e.txt", nil)
	calls := s.Calls(drivetest.CallUpdate)
	_, err := w.Update(ctx, id, provider.Change{Parent: to})
	require.Error(t, err)
	assert.Equal(t, calls, s.Calls(drivetest.CallUpdate))

	_, err = w.Update(ctx, "missing", provider.Change{Name: "x"})
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	_, err = w.Update(ctx, id, provider.Change{Parent: "missing", OldParent: from})
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
}

func TestTrash(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	id := s.AddFile("", "old.txt", []byte("x"))

	require.NoError(t, w.Trash(ctx, id))
	_, err := d.Get(ctx, id)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	page, err := d.Children(ctx, s.RootID(), "")
	require.NoError(t, err)
	assert.Empty(t, page.Items)

	s.Delete(id)
	err = w.Trash(ctx, id)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
}

// A change is not sent again after an answer that leaves open whether Drive made it.
// A clear "slow down" is waited for and sent again.
func TestChangesNotRepeated(t *testing.T) {
	s, _, w, _ := setupWriter(t)
	ctx := context.Background()
	id := s.AddFile("", "a.txt", nil)
	base := provider.RetryBase
	provider.RetryBase = time.Millisecond
	defer func() { provider.RetryBase = base }()

	s.Fail(http.StatusInternalServerError, "backendError")
	_, err := w.CreateFolder(ctx, s.RootID(), "x")
	assert.Equal(t, provider.KindBusy, provider.KindOf(err))
	assert.Equal(t, 1, s.Calls(drivetest.CallCreate))

	s.Fail(http.StatusServiceUnavailable, "")
	_, err = w.Update(ctx, id, provider.Change{Name: "b.txt"})
	assert.Equal(t, provider.KindBusy, provider.KindOf(err))
	assert.Equal(t, 1, s.Calls(drivetest.CallUpdate))

	s.Fail(http.StatusForbidden, "backendError")
	err = w.Trash(ctx, id)
	assert.Equal(t, provider.KindBusy, provider.KindOf(err))
	assert.Equal(t, 2, s.Calls(drivetest.CallUpdate))

	s.Throttle(1)
	s.Fail(http.StatusForbidden, "userRateLimitExceeded")
	_, err = w.CreateEmpty(ctx, s.RootID(), "y")
	require.NoError(t, err)
	assert.Equal(t, 4, s.Calls(drivetest.CallCreate))

	s.Reject()
	require.NoError(t, w.Trash(ctx, id))
	assert.Equal(t, 4, s.Calls(drivetest.CallUpdate))
}

func TestNewUploadInRanges(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	s.CheckRanges(true)
	parent := s.AddFolder("", "Backup")
	content := pattern(2*drivetest.Granularity + 1000)
	size := int64(len(content))

	u, err := w.NewUpload(ctx, parent, "archive.bin", size)
	require.NoError(t, err)
	g := u.Granularity()
	assert.Equal(t, int64(drivetest.Granularity), g)

	item, err := send(t, u, content, 0, g)
	require.NoError(t, err)
	assert.Nil(t, item)
	received, done, err := u.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, g, received)
	assert.Nil(t, done)
	// Nothing is there under the name before the last range.
	found, err := d.Named(ctx, parent, "archive.bin")
	require.NoError(t, err)
	assert.Empty(t, found)

	item, err = send(t, u, content, g, g)
	require.NoError(t, err)
	assert.Nil(t, item)
	receipt, err := send(t, u, content, 2*g, size-2*g)
	require.NoError(t, err)
	require.NotNil(t, receipt)

	stored, err := d.Get(ctx, receipt.ID)
	require.NoError(t, err)
	assert.Equal(t, stored, receipt)
	assert.Equal(t, "archive.bin", receipt.Name)
	assert.False(t, receipt.IsDir)
	assert.Equal(t, size, receipt.Size)
	assert.Equal(t, md5Hex(content), receipt.Hash)
	assert.NotEmpty(t, receipt.Revision)
	assert.Equal(t, "r:"+receipt.Revision, receipt.Version)
	assert.False(t, receipt.ModTime.IsZero())
	c, err := d.Open(ctx, receipt, "")
	require.NoError(t, err)
	assert.Equal(t, string(content), read(t, c))

	received, done, err = u.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, size, received)
	assert.Equal(t, receipt, done)

	assert.Equal(t, 1, s.Calls(drivetest.CallStart))
	assert.Equal(t, 3, s.Calls(drivetest.CallRange))
	assert.Equal(t, 2, s.Calls(drivetest.CallStatus))
}

func TestReplaceUpload(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	id := s.AddFile("", "report.txt", []byte("old content"))
	old, err := d.Get(ctx, id)
	require.NoError(t, err)

	content := []byte("the new content")
	u, err := w.ReplaceUpload(ctx, old, int64(len(content)))
	require.NoError(t, err)
	receipt, err := send(t, u, content, 0, int64(len(content)))
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, id, receipt.ID)
	assert.Equal(t, "report.txt", receipt.Name)
	assert.Equal(t, int64(len(content)), receipt.Size)
	assert.Equal(t, md5Hex(content), receipt.Hash)
	assert.NotEqual(t, old.Revision, receipt.Revision)
	assert.Equal(t, "r:"+receipt.Revision, receipt.Version)
	assert.True(t, receipt.ModTime.After(old.ModTime))

	// The content before stays readable by its revision.
	c, err := d.Open(ctx, old, "")
	require.NoError(t, err)
	assert.Equal(t, "old content", read(t, c))
	c, err = d.Open(ctx, receipt, "")
	require.NoError(t, err)
	assert.Equal(t, string(content), read(t, c))

	_, err = w.ReplaceUpload(ctx, &provider.Item{ID: "missing", Size: 1}, 4)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	folder, err := d.Get(ctx, s.AddFolder("", "Folder"))
	require.NoError(t, err)
	_, err = w.ReplaceUpload(ctx, folder, 4)
	assert.Equal(t, provider.KindForbidden, provider.KindOf(err))
	assert.Equal(t, 2, s.Calls(drivetest.CallStart))
}

func TestRangeRefusals(t *testing.T) {
	s, _, w, _ := setupWriter(t)
	ctx := context.Background()
	content := pattern(drivetest.Granularity + 10)
	size := int64(len(content))

	// Size 0 is no session.
	_, err := w.NewUpload(ctx, s.RootID(), "empty", 0)
	require.Error(t, err)
	assert.Equal(t, 0, s.Calls(drivetest.CallStart))

	u, err := w.NewUpload(ctx, s.RootID(), "data.bin", size)
	require.NoError(t, err)

	// A range beyond the announced size is not sent.
	_, err = send(t, u, append(content, 0), 0, size+1)
	require.Error(t, err)
	assert.Equal(t, 0, s.Calls(drivetest.CallRange))

	// Drive refuses a range that is not a multiple of the granularity.
	s.CheckRanges(true)
	_, err = send(t, u, content, 0, 1000)
	noAddress(t, s, err)
	assert.Equal(t, provider.KindOther, provider.KindOf(err))
	received, _, err := u.State(ctx)
	require.NoError(t, err)
	assert.Zero(t, received)

	// An error of Drive's own is reported, not repeated: the outcome is open.
	s.Fail(http.StatusInternalServerError, "backendError")
	_, err = send(t, u, content, 0, 1000)
	noAddress(t, s, err)
	assert.Equal(t, provider.KindBusy, provider.KindOf(err))
	s.Throttle(1)
	_, err = send(t, u, content, 0, 1000)
	assert.Equal(t, provider.KindBusy, provider.KindOf(err))
	assert.Equal(t, 3, s.Calls(drivetest.CallRange))

	item, err := send(t, u, content, 0, drivetest.Granularity)
	require.NoError(t, err)
	assert.Nil(t, item)
	item, err = send(t, u, content, drivetest.Granularity, 10)
	require.NoError(t, err)
	assert.Equal(t, size, item.Size)
}

// A range breaks off midway: Drive keeps what arrived, State tells how much, and the
// rest follows from there.
func TestRangeBreaksOff(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	content := pattern(16)

	u, err := w.NewUpload(ctx, s.RootID(), "part.bin", 16)
	require.NoError(t, err)
	s.BreakNextRange(4)
	_, err = send(t, u, content, 0, 10)
	noAddress(t, s, err)
	assert.Equal(t, 1, s.Calls(drivetest.CallRange), "not sent again")

	received, done, err := u.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(4), received)
	assert.Nil(t, done)

	receipt, err := send(t, u, content, 4, 12)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, md5Hex(content), receipt.Hash)
	c, err := d.Open(ctx, receipt, "")
	require.NoError(t, err)
	assert.Equal(t, string(content), read(t, c))
}

// The answer to the last range gets lost although Drive stored the file: State hands
// out the receipt.
func TestLostAnswerToLastRange(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	content := pattern(20)

	u, err := w.NewUpload(ctx, s.RootID(), "last.bin", 20)
	require.NoError(t, err)
	_, err = send(t, u, content, 0, 8)
	require.NoError(t, err)
	s.LoseNextAnswer()
	item, err := send(t, u, content, 8, 12)
	noAddress(t, s, err)
	assert.Nil(t, item)
	assert.Equal(t, 2, s.Calls(drivetest.CallRange))

	received, done, err := u.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(20), received)
	require.NotNil(t, done)
	assert.Equal(t, "last.bin", done.Name)
	assert.Equal(t, int64(20), done.Size)
	assert.Equal(t, md5Hex(content), done.Hash)
	assert.Equal(t, "r:"+done.Revision, done.Version)
	stored, err := d.Get(ctx, done.ID)
	require.NoError(t, err)
	assert.Equal(t, stored, done)
}

func TestCancelAndForgottenSession(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	content := pattern(10)

	u, err := w.NewUpload(ctx, s.RootID(), "cancelled.bin", 10)
	require.NoError(t, err)
	_, err = send(t, u, content, 0, 5)
	require.NoError(t, err)
	require.NoError(t, u.Cancel(ctx))
	_, _, err = u.State(ctx)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	noAddress(t, s, err)
	require.NoError(t, u.Cancel(ctx), "already gone")
	assert.Equal(t, 2, s.Calls(drivetest.CallCancel))

	u, err = w.NewUpload(ctx, s.RootID(), "forgotten.bin", 10)
	require.NoError(t, err)
	s.ForgetSessions()
	_, err = send(t, u, content, 0, 10)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	noAddress(t, s, err)
	_, _, err = u.State(ctx)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	require.NoError(t, u.Cancel(ctx))

	page, err := d.Children(ctx, s.RootID(), "")
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

func TestStorageFull(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	content := pattern(10)

	u, err := w.NewUpload(ctx, s.RootID(), "big.bin", 10)
	require.NoError(t, err)
	_, err = send(t, u, content, 0, 4)
	require.NoError(t, err)

	s.SetFull(true)
	_, err = send(t, u, content, 4, 6)
	assert.Equal(t, provider.KindFull, provider.KindOf(err))
	noAddress(t, s, err)
	received, _, err := u.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(4), received)

	_, err = w.NewUpload(ctx, s.RootID(), "more.bin", 10)
	assert.Equal(t, provider.KindFull, provider.KindOf(err))
	_, err = w.CreateFolder(ctx, s.RootID(), "More")
	assert.Equal(t, provider.KindFull, provider.KindOf(err))
	_, err = w.CreateEmpty(ctx, s.RootID(), "more.txt")
	assert.Equal(t, provider.KindFull, provider.KindOf(err))

	page, err := d.Children(ctx, s.RootID(), "")
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

// A range refused for its token is an open outcome. The token is renewed, and the
// calls after it carry the new one.
func TestRangeTokenRenewal(t *testing.T) {
	s, _, w, auth := setupWriter(t)
	ctx := context.Background()
	content := pattern(10)

	u, err := w.NewUpload(ctx, s.RootID(), "token.bin", 10)
	require.NoError(t, err)
	s.Reject()
	_, err = send(t, u, content, 0, 10)
	noAddress(t, s, err)
	assert.Equal(t, provider.KindOther, provider.KindOf(err))
	assert.Equal(t, 1, auth.Renewals())

	received, _, err := u.State(ctx)
	require.NoError(t, err)
	assert.Zero(t, received)
	item, err := send(t, u, content, 0, 10)
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, []string{"token-1", "token-1", "token-2", "token-2"}, s.Tokens())
}

// The session address is used only on the host of the API, where the token may go.
func TestUploadAddressStaysAtAPIHost(t *testing.T) {
	s, d, w, _ := setupWriter(t)
	ctx := context.Background()
	s.SessionsElsewhere(true)

	_, err := w.NewUpload(ctx, s.RootID(), "away.bin", 10)
	assert.Equal(t, provider.KindUnsafe, provider.KindOf(err))
	noAddress(t, s, err)
	id := s.AddFile("", "here.bin", []byte("x"))
	item, err := d.Get(ctx, id)
	require.NoError(t, err)
	_, err = w.ReplaceUpload(ctx, item, 10)
	assert.Equal(t, provider.KindUnsafe, provider.KindOf(err))
	assert.Empty(t, s.ContentTokens(), "nothing reached the other host")
}

func TestWriteReasons(t *testing.T) {
	for _, reason := range []string{"activeItemCreationLimitExceeded", "numChildrenInNonRootLimitExceeded", "teamDriveFileLimitExceeded"} {
		e := classify(403, nil, []byte(`{"error":{"errors":[{"reason":"`+reason+`"}]}}`))
		assert.Equal(t, provider.KindFull, e.Kind, reason)
	}
	// Only a clear "slow down" stays busy for a change.
	assert.Equal(t, provider.KindBusy, classifyOnce(429, nil, nil).Kind)
	assert.Equal(t, provider.KindBusy, classifyOnce(403, nil, []byte(`{"error":{"errors":[{"reason":"rateLimitExceeded"}]}}`)).Kind)
	assert.Equal(t, provider.KindOther, classifyOnce(503, nil, nil).Kind)
	assert.Equal(t, provider.KindFull, classifyOnce(403, nil, []byte(`{"error":{"errors":[{"reason":"storageQuotaExceeded"}]}}`)).Kind)
}
