package routers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/gin-gonic/gin"
)

// clientIP sends one request from peer with the given headers and returns what gin sees.
func clientIP(t *testing.T, trusted, peer string, headers map[string]string) string {
	t.Helper()
	t.Setenv(TrustedProxiesEnv, trusted)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	trustProxies(r, logging.NewConsoleLogger(logging.LevelError), "")
	var got string
	r.GET("/", func(c *gin.Context) { got = c.ClientIP() })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer + ":40000"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClientIPWithoutTrustedProxies(t *testing.T) {
	got := clientIP(t, "", "203.0.113.7", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
		"X-Real-IP":       "5.6.7.8",
	})
	if got != "203.0.113.7" {
		t.Fatalf("forwarding headers must be ignored when no proxy is trusted, got %s", got)
	}
}

func TestClientIPBehindTwoProxies(t *testing.T) {
	// Client 198.51.100.9 sent a forged entry; the VPS (10.8.0.1, over WireGuard) appended the
	// real client, the NAS proxy (172.18.0.2) appended the VPS.
	got := clientIP(t, "10.8.0.0/24, 172.18.0.0/16", "172.18.0.2", map[string]string{
		"X-Forwarded-For": "1.2.3.4, 198.51.100.9, 10.8.0.1",
	})
	if got != "198.51.100.9" {
		t.Fatalf("want the real client, got %s", got)
	}
}

func TestClientIPIgnoresRealIPHeader(t *testing.T) {
	got := clientIP(t, "172.18.0.0/16", "172.18.0.2", map[string]string{"X-Real-IP": "1.2.3.4"})
	if got != "172.18.0.2" {
		t.Fatalf("X-Real-IP must not be believed, got %s", got)
	}
}

func TestClientIPFromUntrustedPeer(t *testing.T) {
	got := clientIP(t, "172.18.0.0/16", "203.0.113.7", map[string]string{"X-Forwarded-For": "1.2.3.4"})
	if got != "203.0.113.7" {
		t.Fatalf("a peer that is not a listed proxy must not set the address, got %s", got)
	}
}

func TestClientIPWithInvalidList(t *testing.T) {
	got := clientIP(t, "not-an-address", "203.0.113.7", map[string]string{"X-Forwarded-For": "1.2.3.4"})
	if got != "203.0.113.7" {
		t.Fatalf("an invalid list must trust nobody, got %s", got)
	}
}
