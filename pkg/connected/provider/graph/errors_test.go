package graph

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
		kind   provider.Kind
	}{
		{"itemNotFound", 400, provider.KindNotFound},
		{"nameAlreadyExists", 400, provider.KindExists},
		{"accessDenied", 400, provider.KindForbidden},
		{"quotaLimitReached", 400, provider.KindFull},
		{"activityLimitReached", 429, provider.KindBusy},
		{"serviceNotAvailable", 503, provider.KindBusy},
		{"generalException", 500, provider.KindBusy},
		{"generalException", 400, provider.KindOther},
		{"resourceModified", 409, provider.KindStale},
		{"preconditionFailed", 400, provider.KindStale},
		{"malwareDetected", 403, provider.KindFlagged},
		{"virusSuspicious", 403, provider.KindFlagged},
		{"unexpected", 410, provider.KindNotFound},
		{"unexpected", 412, provider.KindStale},
		{"accessDenied", 412, provider.KindStale},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.code, tc.status), func(t *testing.T) {
			body := fmt.Appendf(nil, `{"error":{"code":%q,"message":"private detail for Alex"}}`, tc.code)
			err := classify(tc.status, nil, body)
			require.Equal(t, &provider.Error{Kind: tc.kind, Status: tc.status, Code: tc.code}, err)
			require.NotContains(t, err.Error(), "private detail for Alex")
		})
	}
}

func TestClassifyNestedCodesAndMalformedBody(t *testing.T) {
	body := []byte(`{"error":{"code":"accessDenied","message":"private detail","innerError":
		{"code":"unfamiliar","innererror":{"code":"malwareDetected","innerError":{"code":"unfamiliar"}}}}}`)
	err := classify(http.StatusForbidden, nil, body)
	require.Equal(t, provider.KindFlagged, err.Kind)
	require.Equal(t, "malwareDetected", err.Code)
	require.NotContains(t, err.Error(), "private detail")

	for _, body := range []string{`{"error":`, `{"error":{"code":"private detail for Alex"}}`} {
		err := classify(http.StatusInsufficientStorage, nil, []byte(body))
		require.Equal(t, provider.KindFull, err.Kind)
		require.Empty(t, err.Code)
	}
}
