package graph

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuickXorHash(t *testing.T) {
	for _, tc := range []struct {
		name, data, hash string
	}{
		{"empty", "", "AAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		{"single byte", "J", "SgAAAAAAAAAAAAAAAQAAAAAAAAA="},
		{"wraps across the last byte", "0123456789abcdef", "8YSBDGZAgxrY4AY42EEYxDAGMpQ="},
		{"length uses two bytes", strings.Repeat("Alex", 80), "AAAAAAAAAAAAAAAAQAEAAAAAAAA="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.hash, QuickXorHash([]byte(tc.data)))
		})
	}
}
