package routers

import (
	"os"
	"strings"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/gin-gonic/gin"
)

// TrustedProxiesEnv lists the reverse proxies (IPs or CIDRs, comma separated) whose
// X-Forwarded-For header the server believes (Fylvo).
const TrustedProxiesEnv = "FYLVO_TRUSTED_PROXIES"

// forwardedHeader is the only header the server reads the client address from.
const forwardedHeader = "X-Forwarded-For"

// trustProxies makes c.ClientIP() safe. Without FYLVO_TRUSTED_PROXIES no forwarding header is
// believed and the client address is the TCP peer. With it, only X-Forwarded-For sent by one of
// the listed proxies counts; gin reads it from the right and stops at the first address that is
// not a listed proxy, so an address the client wrote in front of the list is ignored.
//
// The ProxyHeader setting of conf.ini (gin's TrustedPlatform) still wins over all of this and
// believes its header from anybody, so a warning is logged when it is set.
func trustProxies(r *gin.Engine, l logging.Logger, proxyHeader string) {
	if proxyHeader != "" {
		l.Warning("ProxyHeader %q is set in the config: the server believes it from every client. Prefer %s.",
			proxyHeader, TrustedProxiesEnv)
	}

	r.RemoteIPHeaders = []string{forwardedHeader}
	proxies := splitList(os.Getenv(TrustedProxiesEnv))
	if len(proxies) > 0 {
		err := r.SetTrustedProxies(proxies)
		if err == nil {
			l.Info("Trusting %s from %s.", forwardedHeader, strings.Join(proxies, ", "))
			return
		}
		l.Error("Invalid %s %q, trusting no proxy: %s", TrustedProxiesEnv, os.Getenv(TrustedProxiesEnv), err)
	} else {
		l.Info("%s is not set: forwarding headers are ignored, the client address is the direct peer.", TrustedProxiesEnv)
	}

	_ = r.SetTrustedProxies(nil)
	r.Use(warnIgnoredForwarding(l))
}

// warnIgnoredForwarding logs once when a request carries X-Forwarded-For that is ignored, which
// usually means the server runs behind a proxy and FYLVO_TRUSTED_PROXIES is missing.
func warnIgnoredForwarding(l logging.Logger) gin.HandlerFunc {
	var once sync.Once
	return func(c *gin.Context) {
		if c.GetHeader(forwardedHeader) != "" {
			once.Do(func() {
				l.Warning("A request came with %s, which is ignored. Set %s to the address of the reverse proxy.",
					forwardedHeader, TrustedProxiesEnv)
			})
		}
		c.Next()
	}
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
