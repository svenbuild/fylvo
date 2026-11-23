package graph

import "encoding/base64"

// QuickXorHash returns the OneDrive checksum of data in base64.
func QuickXorHash(data []byte) string {
	const width = 160
	var hash [width / 8]byte
	bit := 0
	for _, value := range data {
		index, shift := bit/8, bit%8
		hash[index] ^= value << shift
		hash[(index+1)%len(hash)] ^= value >> (8 - shift)
		bit = (bit + 11) % width
	}
	// The length is XORed into the last eight bytes in little-endian order.
	length := uint64(len(data))
	for i := len(hash) - 8; i < len(hash); i++ {
		hash[i] ^= byte(length)
		length >>= 8
	}
	return base64.StdEncoding.EncodeToString(hash[:])
}
