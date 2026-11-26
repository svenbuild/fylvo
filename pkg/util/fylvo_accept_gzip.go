package util

import (
	"strconv"
	"strings"
)

// AcceptsGzip reports whether an Accept-Encoding header allows gzip: named (or "*") with a
// weight above 0.
func AcceptsGzip(header string) bool {
	accepted := false
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(part, ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "*" {
			continue
		}
		weight := 1.0
		for _, param := range strings.Split(params, ";") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(param), "q="); ok {
				weight, _ = strconv.ParseFloat(value, 64)
			}
		}
		if name == "gzip" {
			return weight > 0
		}
		accepted = weight > 0
	}
	return accepted
}
