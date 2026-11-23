package routers

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/connectedtest"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: the pages a browser reaches while a storage is being connected answer by
// themselves. Nothing of a signed-in session stands before them: a browser that sends
// a sign-in that is no good still gets the page, and an answer that only says "this is
// loaded ahead" stays empty.
func TestConnectedPagesNeedNoSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	client := connectedtest.Database(t, logger, kv)
	hasher, err := hashid.New("fylvo-test")
	require.NoError(t, err)
	dep := dependency.NewDependency(
		dependency.WithLogger(logger),
		dependency.WithConfigPath(filepath.Join(t.TempDir(), "conf.ini")),
		dependency.WithDbClient(client),
		dependency.WithKV(kv),
		dependency.WithHashIDEncoder(hasher),
	)
	ctx := context.WithValue(context.Background(), dependency.DepCtx{}, dep)
	require.NoError(t, dep.SettingClient().Set(ctx, map[string]string{"siteURL": "https://cloud.example.com"}))
	require.NoError(t, kv.Delete(setting.KvSettingPrefix, "siteURL"))
	engine := InitRouter(dep)

	request := func(method, target string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.Host = "cloud.example.com"
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}
	start := "/api/v4/fylvo/connected/start/" + strings.Repeat("A", 43)
	badSession := map[string]string{"Authorization": "Bearer not-a-token"}

	// A route with a session answers such a request with an error of the API.
	rec := request("GET", "/api/v4/fylvo/connected", badSession)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	for _, method := range []string{"GET", "POST"} {
		rec = request(method, start, badSession)
		require.Equal(t, http.StatusGone, rec.Code, method)
		require.Contains(t, rec.Header().Get("Content-Type"), "text/html", method)
		require.Contains(t, rec.Body.String(), "This link is no longer valid.", method)
		require.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"), method)
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"), method)
		require.Empty(t, rec.Result().Cookies(), method)
	}
	rec = request("GET", start, map[string]string{"Sec-Purpose": "prefetch", "Authorization": "Bearer not-a-token"})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, rec.Body.String())

	// The preview of a file in a connected storage: its grant is the permission. A
	// sign-in that is no good changes nothing, and what is refused is never kept.
	for _, headers := range []map[string]string{nil, badSession} {
		rec = request("GET", "/api/v4/fylvo/connected/thumb/"+strings.Repeat("A", 60)+"/thumb.jpg", headers)
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Empty(t, rec.Body.String())
		require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
		require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		require.Equal(t, "sandbox", rec.Header().Get("Content-Security-Policy"))
	}

	// A POST that is not the button is answered at once. The server does not wait for
	// a body the client announces and then does not send.
	server := httptest.NewServer(engine)
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("POST " + start + " HTTP/1.1\r\nHost: cloud.example.com\r\n" +
		"Content-Type: application/x-www-form-urlencoded\r\nContent-Length: 100000\r\n" +
		"Sec-Fetch-Site: same-origin\r\n\r\nconfirm="))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	answer, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "the answer waits for the rest of the body")
	require.Equal(t, http.StatusGone, answer.StatusCode)
	require.True(t, answer.Close, "the connection ends with the answer")

	// The callback: a page for a sign-in from an app, the redirect into the web app
	// for one from there.
	rec = request("GET", "/api/v4/fylvo/connected/callback?state=a.gone&code=x", badSession)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `href="fylvo://connected"`)
	rec = request("GET", "/api/v4/fylvo/connected/callback?state=gone&code=x", badSession)
	require.Equal(t, http.StatusFound, rec.Code)
	require.True(t, strings.HasPrefix(rec.Header().Get("Location"), "https://cloud.example.com/settings?"), rec.Header().Get("Location"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}
