package explorer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStableThumbExpire(t *testing.T) {
	morning := time.Date(2026, 11, 23, 0, 0, 1, 0, time.UTC)
	evening := time.Date(2026, 11, 23, 23, 59, 59, 0, time.UTC)
	// The whole day gets one address.
	require.Equal(t, StableThumbExpire(morning), StableThumbExpire(evening))
	require.Equal(t, time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC), StableThumbExpire(evening))
	// Valid for 24 to 48 hours.
	require.LessOrEqual(t, StableThumbExpire(morning).Sub(morning), 48*time.Hour)
	require.GreaterOrEqual(t, StableThumbExpire(evening).Sub(evening), 24*time.Hour)
	// Other time zones give the same address at the same moment.
	zurich := time.FixedZone("CET", 3600)
	require.Equal(t, StableThumbExpire(evening), StableThumbExpire(evening.In(zurich)))
}
