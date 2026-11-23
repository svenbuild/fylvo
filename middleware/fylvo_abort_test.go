package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A handler that gives up in the middle of an answer without a length must not look
// like one that finished: the client would keep a cut-off file for the whole.
func TestRecoveryLetsAHandlerAbort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(Recovery())
	engine.GET("/whole", func(c *gin.Context) {
		_, _ = c.Writer.Write([]byte("all of it"))
		c.Writer.Flush()
	})
	engine.GET("/cut", func(c *gin.Context) {
		_, _ = c.Writer.Write([]byte("only the first p"))
		c.Writer.Flush()
		panic(http.ErrAbortHandler)
	})
	server := httptest.NewServer(engine)
	defer server.Close()

	res, err := http.Get(server.URL + "/whole")
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "all of it", string(body))

	res, err = http.Get(server.URL + "/cut")
	require.NoError(t, err)
	_, err = io.ReadAll(res.Body)
	res.Body.Close()
	require.Error(t, err, "the answer ended as if it were complete")
}
