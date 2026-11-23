package auth

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const signExpiryKey = "fylvo_sign_expiry"

// SetSignExpiry keeps the expiry of a checked signed address for the handler (Fylvo); the
// check removes the signature from the request.
func SetSignExpiry(c *gin.Context, sign string) {
	i := strings.LastIndex(sign, ":")
	if i < 0 {
		return
	}
	if expires, err := strconv.ParseInt(sign[i+1:], 10, 64); err == nil && expires > 0 {
		c.Set(signExpiryKey, time.Unix(expires, 0))
	}
}

// SignExpiry returns when the signed address of this request expires; false if it does not.
func SignExpiry(c *gin.Context) (time.Time, bool) {
	v, ok := c.Get(signExpiryKey)
	if !ok {
		return time.Time{}, false
	}
	t, ok := v.(time.Time)
	return t, ok
}
