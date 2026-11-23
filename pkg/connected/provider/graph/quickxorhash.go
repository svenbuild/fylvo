package graph

import (
	"encoding/base64"
	"hash"
)

const quickXorWidth = 160

// QuickXor counts the OneDrive checksum of the bytes written to it.
type QuickXor struct {
	sum    [quickXorWidth / 8]byte
	bit    int
	length uint64
}

var _ hash.Hash = (*QuickXor)(nil)

func NewQuickXor() *QuickXor { return &QuickXor{} }

func (q *QuickXor) Write(p []byte) (int, error) {
	for _, value := range p {
		index, shift := q.bit/8, q.bit%8
		q.sum[index] ^= value << shift
		q.sum[(index+1)%len(q.sum)] ^= value >> (8 - shift)
		q.bit = (q.bit + 11) % quickXorWidth
	}
	q.length += uint64(len(p))
	return len(p), nil
}

// Sum appends the checksum of what was written so far to b.
func (q *QuickXor) Sum(b []byte) []byte {
	sum := q.sum
	// The length is XORed into the last eight bytes in little-endian order.
	length := q.length
	for i := len(sum) - 8; i < len(sum); i++ {
		sum[i] ^= byte(length)
		length >>= 8
	}
	return append(b, sum[:]...)
}

func (q *QuickXor) Reset()         { *q = QuickXor{} }
func (q *QuickXor) Size() int      { return quickXorWidth / 8 }
func (q *QuickXor) BlockSize() int { return 64 }

// Base64 returns the checksum the way OneDrive reports it.
func (q *QuickXor) Base64() string {
	return base64.StdEncoding.EncodeToString(q.Sum(nil))
}

// QuickXorHash returns the OneDrive checksum of data in base64.
func QuickXorHash(data []byte) string {
	q := NewQuickXor()
	_, _ = q.Write(data)
	return q.Base64()
}
