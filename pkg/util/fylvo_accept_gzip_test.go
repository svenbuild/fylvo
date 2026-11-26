package util

import "testing"

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                     false,
		"gzip":                 true,
		"gzip, deflate, br":    true,
		"br;q=1.0, GZIP;q=0.5": true,
		"gzip;q=0":             false,
		"gzip; q=0.0, br":      false,
		"deflate, br":          false,
		"*":                    true,
		"*;q=0":                false,
		"gzip;q=0, *":          false,
		"*, gzip;q=0":          false,
		"identity":             false,
	}
	for header, want := range cases {
		if got := AcceptsGzip(header); got != want {
			t.Errorf("AcceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
