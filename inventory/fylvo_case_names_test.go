package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFoldNameFollowsWindows(t *testing.T) {
	same := [][2]string{
		{"foto.jpg", "FOTO.JPG"},
		{"Bär.txt", "BÄR.TXT"},
		{"ärger", "ÄRGER"},
		{"strasse", "STRASSE"},
	}
	for _, pair := range same {
		require.Equal(t, FoldName(pair[0]), FoldName(pair[1]), pair)
	}

	different := [][2]string{
		{"straße", "STRASSE"},                // ß stays ß, it does not become SS
		{"Müller", "Müller"},                // decomposed and composed ü are two names on NTFS
		{"\U0001F600.txt", "\U0001F601.txt"}, // outside the basic plane: as it is
		{"a_b", "a-b"},
	}
	for _, pair := range different {
		require.NotEqual(t, FoldName(pair[0]), FoldName(pair[1]), pair)
	}
	require.Equal(t, "\U0001F600.TXT", FoldName("\U0001F600.txt"))
}

func TestCaseTwinPatternFindsEveryTwin(t *testing.T) {
	require.Equal(t, `Foto\_1.jpg`, caseTwinPattern("Foto_1.jpg"))
	require.Equal(t, `_mg\%1.jpg`, caseTwinPattern("img%1.jpg")) // i and s become _: ı and ſ are I and S in upper case
	require.Equal(t, `B_r.txt`, caseTwinPattern("Bär.txt"))
	require.Equal(t, `_tra_e`, caseTwinPattern("straße"))
}
