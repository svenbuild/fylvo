package connectedtest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// databaseVersion is the version the test databases are set up as.
const databaseVersion = "4.19.1"

// installed is the database of a fresh installation, as the bytes of its file. Setting
// one up from nothing takes about a second, which adds up over a few hundred tests. So
// it is set up once per test run, and every test gets a copy of its own.
var installed struct {
	once  sync.Once
	bytes []byte
	err   error
}

func install(logger logging.Logger) ([]byte, error) {
	dir, err := os.MkdirTemp("", "fylvo-test-db")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "fylvo.db")
	client, err := ent.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	if _, err := inventory.InitializeDBClient(logger, client, cache.NewMemoStore("", logger), databaseVersion); err != nil {
		_ = client.Close()
		return nil, err
	}
	// Closed before it is read: only then all of it is in the one file.
	if err := client.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Database returns the database of a fresh installation for one test: its own file,
// gone with the test. kv is the cache the installation of the test uses.
func Database(t *testing.T, logger logging.Logger, kv cache.Driver) *ent.Client {
	t.Helper()
	installed.once.Do(func() { installed.bytes, installed.err = install(logger) })
	require.NoError(t, installed.err)

	path := filepath.Join(t.TempDir(), "fylvo.db")
	require.NoError(t, os.WriteFile(path, installed.bytes, 0o600))
	client, err := ent.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	// On an installed database this only runs the checks every start runs.
	client, err = inventory.InitializeDBClient(logger, client, kv, databaseVersion)
	require.NoError(t, err)
	return client
}
