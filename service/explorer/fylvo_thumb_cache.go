package explorer

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager/entitysource"
	"github.com/gin-gonic/gin"
)

// thumbCacheMaxAge is the longest a browser or app keeps a thumbnail image (Fylvo).
const thumbCacheMaxAge = 24 * time.Hour

// StableThumbExpire is the expiry of a thumbnail address issued at now (Fylvo): the end of the
// next UTC day. All requests of one day get the same address, so the image is loaded once a day,
// and an address is valid for 24 to 48 hours.
func StableThumbExpire(now time.Time) time.Time {
	day := now.UTC().Truncate(24 * time.Hour)
	return day.Add(48 * time.Hour)
}

// prepareThumbResponse sets the headers for the image of a stored thumbnail (Fylvo): private
// caching no longer than the signed address is valid, and the content type of the real format
// (thumbnails are JPEG or PNG, but all are named ".jpg").
func prepareThumbResponse(c *gin.Context, es entitysource.EntitySource) {
	maxAge := thumbCacheMaxAge
	if expires, ok := auth.SignExpiry(c); ok {
		maxAge = min(maxAge, time.Until(expires))
	}
	c.Header("Cache-Control", fmt.Sprintf("private, max-age=%d", max(0, int(maxAge.Seconds()))))

	var head [512]byte
	n, _ := io.ReadFull(es, head[:])
	if _, err := es.Seek(0, io.SeekStart); err == nil && n > 0 {
		c.Header("Content-Type", http.DetectContentType(head[:n]))
	}
}

func isThumbEntity(es entitysource.EntitySource) bool {
	return es.Entity().Type() == types.EntityTypeThumbnail
}
