package logging

import (
	"net/url"
	"regexp"
	"strings"
)

// Fylvo: log lines must not carry secrets. Request addresses often do (the WOPI access_token,
// signed links, the password reset secret, a share password in /s/<id>/<password>). Request
// passes the address of every request log line through RedactURL, and every log line goes
// through RedactText, which catches full addresses in error messages.

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
	// Fylvo: the code that completes connecting a storage.
	"finish": true,
}

// minTokenLength is the length from which an unknown value that looks like a token is replaced.
const minTokenLength = 24

var (
	// tokenValue matches values made only of the characters of tokens, ids and signatures.
	tokenValue = regexp.MustCompile(`^[A-Za-z0-9._~+=:-]+$`)
	// userInfo matches the user and password in an address like scheme://user:password@host,
	// also cloudreve://<share id>:<password>@share, up to the last "@" before the host ends, as
	// Go parses it. Group 1 keeps the scheme.
	userInfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/?#\s]*@`)
	// secretPaths match paths with a secret segment; group 1 is kept before it.
	secretPaths = []*regexp.Regexp{
		// Share link with its password: /s/<id>/<password>
		regexp.MustCompile(`^((?:[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*)?/s/[^/]+/)[^/]+`),
		// Upload callback with its key: /api/v4/callback/<policy>/<session>/<key>
		regexp.MustCompile(`^((?:[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*)?/api/v4/callback/[^/]+/[^/]+/)[^/]+`),
		// Content and thumbnail of a connected storage: the segment after the kind is the
		// grant, which opens the file while it is valid.
		regexp.MustCompile(`^((?:[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*)?/api/v4/fylvo/connected/(?:content|thumb)/)[^/]+`),
		// Start page of connecting a storage from an app: the segment is the ticket,
		// which the first browser that opens it makes its own.
		regexp.MustCompile(`^((?:[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*)?/api/v4/fylvo/connected/start/)[^/]+`),
	}
	// urlInText finds addresses in a text; it stops before trailing punctuation and quotes.
	urlInText = regexp.MustCompile(`(?i)https?://[^\s"<>]*[^\s"'<>.,;:)]`)
)

// RedactURL returns a URL or a path with query for a log line, with secrets replaced by
// Redacted: user and password, secret path segments, secret query values and the fragment.
// Parameter names, the scheme, the host and the path stay readable.
func RedactURL(raw string) string {
	raw, _, hasFragment := strings.Cut(raw, "#")
	path, query, hasQuery := strings.Cut(raw, "?")
	path = userInfo.ReplaceAllString(path, "${1}"+Redacted+"@")
	for _, re := range secretPaths {
		path = re.ReplaceAllString(path, "${1}"+Redacted)
	}
	if hasQuery {
		parts := strings.Split(query, "&")
		for i, part := range parts {
			if name, value, ok := strings.Cut(part, "="); ok && value != "" {
				parts[i] = name + "=" + redactValue(name, value)
			}
		}
		path += "?" + strings.Join(parts, "&")
	}
	if hasFragment {
		path += "#" + Redacted
	}
	return path
}

// RedactText masks user and password of addresses of any scheme (like cloudreve://<share
// id>:<password>@share) and applies RedactURL to every http(s) address in a text.
func RedactText(text string) string {
	text = userInfo.ReplaceAllString(text, "${1}"+Redacted+"@")
	return urlInText.ReplaceAllStringFunc(text, RedactURL)
}

// redactValue returns the raw query value, Redacted, or the value without the password of an
// address inside it (a share URI in /home?path=...).
func redactValue(name, value string) string {
	rawValue := value
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	if secretParams[strings.ToLower(name)] {
		return Redacted
	}
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	if len(value) >= minTokenLength && tokenValue.MatchString(value) &&
		strings.ContainsAny(value, "0123456789") {
		return Redacted
	}
	if userInfo.MatchString(value) {
		escaped := url.QueryEscape(userInfo.ReplaceAllString(value, "${1}"+Redacted+"@"))
		return strings.Replace(escaped, url.QueryEscape(Redacted), Redacted, 1)
	}
	return rawValue
}
