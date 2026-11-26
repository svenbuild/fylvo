package util

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAcceptsEncoding(t *testing.T) {
	cases := map[string]bool{
		"":                     false,
		"gzip":                 true,
		"gzip, deflate, br":    true,
		"br;q=1.0, GZIP;q=0.5": true,
		"gzip;q=0":             false,
		"gzip; q=0.0, br":      false,
		"gzip;q=0.000":         false,
		"deflate, br":          false,
		"*":                    true,
		"*;q=0":                false,
		"gzip;q=0, *":          false,
		"*, gzip;q=0":          false,
		"identity":             false,
	}
	for header, want := range cases {
		require.Equal(t, want, AcceptsEncoding(header, "gzip"), header)
	}
	require.True(t, AcceptsEncoding("gzip, deflate, br, zstd", "br"))
	require.False(t, AcceptsEncoding("gzip", "br"))

	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add("Accept-Encoding", "br")
	r.Header.Add("Accept-Encoding", "gzip")
	require.True(t, AcceptsEncoding(AcceptEncoding(r), "gzip"))
}

func TestVaryByEncoding(t *testing.T) {
	h := http.Header{}
	h.Set("Vary", "Origin")
	VaryByEncoding(h)
	VaryByEncoding(h)
	require.Equal(t, []string{"Origin", "Accept-Encoding"}, h.Values("Vary"))
}
