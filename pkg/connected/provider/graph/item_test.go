package graph

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A file always has a version, whatever the answer leaves out: a download address is
// tied to it.
func TestVersionIsNeverEmpty(t *testing.T) {
	cases := map[string]string{
		`{"id":"a","name":"a","size":3,"file":{"hashes":{"quickXorHash":"abc"}},"cTag":"c1","eTag":"e1"}`: "c:c1",
		`{"id":"a","name":"a","size":3,"file":{"hashes":{"quickXorHash":"abc"}},"eTag":"e1"}`:             "h:abc:3",
		`{"id":"a","name":"a","size":3,"file":{},"eTag":"e1"}`:                                            "e:e1",
		`{"id":"a","name":"a","size":3,"file":{},"lastModifiedDateTime":"2026-01-01T09:00:00Z"}`:          "t:2026-01-01T09:00:00Z:3",
	}
	for raw, want := range cases {
		var entry driveItem
		require.NoError(t, json.Unmarshal([]byte(raw), &entry))
		require.Equal(t, want, entry.asItem().Version, raw)
	}
}
