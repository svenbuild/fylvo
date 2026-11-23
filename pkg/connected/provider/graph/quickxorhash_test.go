package graph

import "testing"

// The checksums were made with another implementation of the algorithm (rclone hashsum
// quickxor), so this one is checked against something that is not itself.
func TestQuickXorHashAgainstKnownValues(t *testing.T) {
	long := make([]byte, 300000)
	for i := range long {
		long[i] = byte((i*7 + i/4099) % 256)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "AAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		{"short", []byte("sample"), "cwhDG+DAhjIAAAAABgAAAAAAAAA="},
		{"long", long, "k+f1kEeHdM/lNHtq5W+uzQl8cQg="},
	} {
		if got := QuickXorHash(tc.data); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
		// Written in pieces it is the same.
		q := NewQuickXor()
		for i := 0; i < len(tc.data); i += 4097 {
			_, _ = q.Write(tc.data[i:min(i+4097, len(tc.data))])
		}
		if got := q.Base64(); got != tc.want {
			t.Errorf("%s in pieces: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
