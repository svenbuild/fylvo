package manager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sort"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// The archives in testdata hold docs/a.txt and b.txt and were packed with the password
// "password": enc_names.7z with 7z a -mhe=on (file names encrypted), enc_content.7z without
// -mhe (only the contents encrypted), enc.zip as ZIP with AES.

func readTestArchive(t *testing.T, name string) *bytes.Reader {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return bytes.NewReader(b)
}

func archiveErrorCode(err error) int {
	var app serializer.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	return 0
}

func names(list []ArchivedFile) []string {
	out := make([]string, 0, len(list))
	for _, f := range list {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

func TestSevenZipEncryptedNames(t *testing.T) {
	r := readTestArchive(t, "enc_names.7z")

	_, err := get7zFileList(context.Background(), r, r.Size(), nil, "")
	require.Equal(t, serializer.CodeArchiveEncrypted, archiveErrorCode(err))

	_, err = get7zFileList(context.Background(), r, r.Size(), nil, "wrong")
	require.Equal(t, serializer.CodeArchiveWrongPassword, archiveErrorCode(err))

	list, err := get7zFileList(context.Background(), r, r.Size(), nil, "password")
	require.NoError(t, err)
	require.Equal(t, []string{"b.txt", "docs", "docs/a.txt"}, names(list))
}

func TestSevenZipEncryptedContentStillListed(t *testing.T) {
	r := readTestArchive(t, "enc_content.7z")
	list, err := get7zFileList(context.Background(), r, r.Size(), nil, "")
	require.NoError(t, err)
	require.Equal(t, []string{"b.txt", "docs", "docs/a.txt"}, names(list))
}

func TestSevenZipDamagedIsNoPasswordError(t *testing.T) {
	garbage := bytes.NewReader([]byte("this is not a 7z archive at all, just some text"))
	_, err := get7zFileList(context.Background(), garbage, garbage.Size(), nil, "")
	require.Error(t, err)
	require.Zero(t, archiveErrorCode(err))

	r := readTestArchive(t, "enc_names.7z")
	cut := sectionOf(r, r.Size()-20)
	_, err = get7zFileList(context.Background(), cut, cut.Size(), nil, "")
	require.Error(t, err)
	require.Zero(t, archiveErrorCode(err))
}

func sectionOf(r *bytes.Reader, size int64) *bytes.Reader {
	b := make([]byte, size)
	_, _ = r.ReadAt(b, 0)
	return bytes.NewReader(b)
}

func TestZipEncryptedContentStillListed(t *testing.T) {
	r := readTestArchive(t, "enc.zip")
	list, err := getZipFileList(context.Background(), r, r.Size(), nil, "")
	require.NoError(t, err)
	require.Equal(t, []string{"b.txt", "docs", "docs/a.txt"}, names(list))
}
