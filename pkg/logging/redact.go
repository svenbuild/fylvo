package logging

import (
	"net/url"
	"regexp"
	"strings"
)

// Fylvo: log lines must not carry secrets. Request addresses often do (the WOPI access_token,
// signed links, the password reset secret, a share password in /s/<id>/<password>), so every
// address goes through RedactURL before it is logged.

// Redacted replaces a secret in a log line.
const Redacted = "***"

// secretParams are query parameters whose values are always replaced (compared in lower case).
var secretParams = map[string]bool{
	"access_token": true, "refresh_token": true, "id_token": true, "token": true,
	"sign": true, "signature": true, "sig": true, "secret": true, "client_secret": true,
	"password": true, "pwd": true, "code": true, "code_verifier": true, "state": true,
	"ticket": true, "key": true, "api_key": true, "apikey": true, "auth": true,
	"authorization": true, "credential": true, "session_id": true, "tempauth": true,
	"x-amz-signature": true, "x-amz-credential": true, "x-amz-security-token": true,
	"q-signature": true, "ossaccesskeyid": true, "security-token": true, "_upt": true,
}

// minTokenLength is the length from which an unknown value that looks like a token is replaced.
const minTokenLength = 24

var (
	// tokenValue matches values made only of the characters of tokens, ids and signatures.
	tokenValue = regexp.MustCompile(`^[A-Za-z0-9._~+=:-]+$`)
	// secretPaths match paths with a secret as the last segment, which is kept in group 1.
	secretPaths = []*regexp.Regexp{
		// Share link with its password: /s/<id>/<password>
		regexp.MustCompile(`^((?:[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*)?/s/[^/]+/)[^/]+$`),
		// Upload callback with its key: /api/v4/callback/<policy>/<session>/<key>
		regexp.MustCompile(`^((?:[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*)?/api/v4/callback/[^/]+/[^/]+/)[^/]+$`),
	}
	urlInText = regexp.MustCompile(`https?://[^\s"']+`)
)

// RedactURL returns a URL or a path with query for a log line, with secret values replaced by
// Redacted. Parameter names, the scheme, the host and the path stay readable.
func RedactURL(raw string) string {
	path, query, hasQuery := strings.Cut(raw, "?")
	for _, re := range secretPaths {
		path = re.ReplaceAllString(path, "${1}"+Redacted)
	}
	if !hasQuery {
		return path
	}

	parts := strings.Split(query, "&")
	for i, part := range parts {
		name, value, hasValue := strings.Cut(part, "=")
		if hasValue && value != "" && isSecret(name, value) {
			parts[i] = name + "=" + Redacted
		}
	}
	return path + "?" + strings.Join(parts, "&")
}

// RedactText applies RedactURL to every http(s) address in a text, for example an error message.
func RedactText(text string) string {
	return urlInText.ReplaceAllStringFunc(text, RedactURL)
}

func isSecret(name, value string) bool {
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	if secretParams[strings.ToLower(name)] {
		return true
	}
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	return len(value) >= minTokenLength && tokenValue.MatchString(value) &&
		strings.ContainsAny(value, "0123456789")
}
