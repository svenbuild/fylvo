package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactURL(t *testing.T) {
	cases := []struct{ in, want string }{
		// No query, nothing secret.
		{"/api/v4/file/list", "/api/v4/file/list"},
		// WOPI call from the office server.
		{"/api/v4/file/wopi/abc/contents?access_token=0b7d1c2e-1111-2222-3333-444455556666.Xy9AbCdEf",
			"/api/v4/file/wopi/abc/contents?access_token=***"},
		// Signed download link: the signature goes, the other parameters stay.
		{"/api/v4/file/content/Xy/0/a.pdf?download=true&sign=AbC%2Bdef%3D%3A1767225600",
			"/api/v4/file/content/Xy/0/a.pdf?download=true&sign=***"},
		// Password reset link, the name is matched without case.
		{"/session/reset?id=lV&Secret=abc", "/session/reset?id=lV&Secret=***"},
		// OAuth parameters.
		{"/session/authorize?client_id=x&code=c&state=s&redirect_uri=http%3A%2F%2Flocalhost",
			"/session/authorize?client_id=x&code=***&state=***&redirect_uri=http%3A%2F%2Flocalhost"},
		// Share password in the query and in the short link.
		{"/api/v4/share/info/Ab?password=hunter2", "/api/v4/share/info/Ab?password=***"},
		{"/s/Ab/hunter2", "/s/Ab/***"},
		{"/s/Ab", "/s/Ab"},
		{"https://cloud.example/s/Ab/hunter2", "https://cloud.example/s/Ab/***"},
		// Upload callback key.
		{"/api/v4/callback/s3/sess-1/k3y", "/api/v4/callback/s3/sess-1/***"},
		// Outgoing S3 link.
		{"https://s3.example/b/o?X-Amz-Credential=AK%2F2026&X-Amz-Signature=ff00&X-Amz-Expires=3600",
			"https://s3.example/b/o?X-Amz-Credential=***&X-Amz-Signature=***&X-Amz-Expires=3600"},
		// An unknown parameter with a long token-like value is masked, short and path-like values stay.
		{"/x?foo=8f3a9c0d1e2b4a5f6c7d8e9f0a1b2c3d&n=42&uri=cloudreve%3A%2F%2Fmy%2FDocuments%2Fvery-long-folder-name-2026",
			"/x?foo=***&n=42&uri=cloudreve%3A%2F%2Fmy%2FDocuments%2Fvery-long-folder-name-2026"},
		// Empty values and flags stay.
		{"/x?sign=&flag", "/x?sign=&flag"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, RedactURL(c.in), c.in)
	}
}

func TestRedactText(t *testing.T) {
	in := `Get "https://s3.example/b/o?X-Amz-Signature=ff00&n=1": dial tcp: timeout`
	assert.Equal(t, `Get "https://s3.example/b/o?X-Amz-Signature=***&n=1": dial tcp: timeout`, RedactText(in))
}
