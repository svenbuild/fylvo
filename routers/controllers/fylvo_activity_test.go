package controllers

import (
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/stretchr/testify/require"
)

func TestLoginMethodFromRoute(t *testing.T) {
	require.Equal(t, activity.MethodPassword, loginMethod("/api/v4/session/token"))
	require.Equal(t, activity.MethodTwoFA, loginMethod("/api/v4/session/token/2fa"))
	require.Equal(t, activity.MethodPasskey, loginMethod("/api/v4/session/authn"))
	require.Equal(t, activity.MethodPassword, loginMethod("/api/v4/session/token/password"))
}
