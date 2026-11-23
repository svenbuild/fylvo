package graph

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

type errorCode struct {
	Code  string     `json:"code"`
	Inner *errorCode `json:"innerError"`
}

var shortCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func classify(status int, header http.Header, body []byte) *provider.Error {
	result := provider.ByStatus(status, header, nil)
	var response struct {
		Error *errorCode `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil {
		return result
	}
	// Graph puts the most specific code deepest in the chain.
	for detail := response.Error; detail != nil; detail = detail.Inner {
		kind := codeKind(detail.Code, status)
		if kind != provider.KindOther {
			result.Kind, result.Code = kind, detail.Code
		} else if result.Code == "" && shortCode.MatchString(detail.Code) {
			result.Code = detail.Code
		}
	}
	if status == http.StatusPreconditionFailed {
		result.Kind = provider.KindStale
	}
	return result
}

func codeKind(code string, status int) provider.Kind {
	switch code {
	case "itemNotFound":
		return provider.KindNotFound
	case "nameAlreadyExists":
		return provider.KindExists
	case "accessDenied":
		return provider.KindForbidden
	case "quotaLimitReached":
		return provider.KindFull
	case "activityLimitReached", "serviceNotAvailable":
		return provider.KindBusy
	case "generalException":
		if status >= 500 && status < 600 {
			return provider.KindBusy
		}
	case "resourceModified", "preconditionFailed":
		return provider.KindStale
	case "malwareDetected", "virusSuspicious":
		return provider.KindFlagged
	}
	return provider.KindOther
}
