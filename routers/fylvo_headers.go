package routers

import "github.com/gin-gonic/gin"

// securityHeaders sets the answer headers that belong in the server (Fylvo). HSTS is set by the
// proxy that terminates HTTPS. Referrer-Policy sends other sites only the origin, so share link
// passwords in the path stay here.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}
