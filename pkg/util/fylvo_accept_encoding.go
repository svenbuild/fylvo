package util

import (
	"net/http"
	"strconv"
	"strings"
)

// AcceptEncoding is the Accept-Encoding of a request, all its header lines joined.
func AcceptEncoding(r *http.Request) string {
	return strings.Join(r.Header.Values("Accept-Encoding"), ",")
}

// AcceptsEncoding reports whether an Accept-Encoding header allows the encoding: named (or
// "*" when it is not named) with a weight above 0.
func AcceptsEncoding(header, encoding string) bool {
	accepted := false
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(part, ";")
		name = strings.TrimSpace(name)
		if !strings.EqualFold(name, encoding) && name != "*" {
			continue
		}
		weight := 1.0
		for _, param := range strings.Split(params, ";") {
			if value, ok := strings.CutPrefix(strings.ReplaceAll(param, " ", ""), "q="); ok {
				weight, _ = strconv.ParseFloat(value, 64)
			}
		}
		if name != "*" {
			return weight > 0
		}
		accepted = weight > 0
	}
	return accepted
}

// VaryByEncoding adds Accept-Encoding to the Vary header unless it is there.
func VaryByEncoding(h http.Header) {
	for _, line := range h.Values("Vary") {
		for _, name := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(name), "Accept-Encoding") {
				return
			}
		}
	}
	h.Add("Vary", "Accept-Encoding")
}
