package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSignExpiry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, ok := SignExpiry(c)
	require.False(t, ok)

	SetSignExpiry(c, "abc:def==:1795600000")
	expires, ok := SignExpiry(c)
	require.True(t, ok)
	require.Equal(t, time.Unix(1795600000, 0), expires)

	// No expiry (0) or a broken signature: none.
	for _, sign := range []string{"abc:0", "abc", "abc:x"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		SetSignExpiry(c, sign)
		_, ok := SignExpiry(c)
		require.False(t, ok, sign)
	}
}
