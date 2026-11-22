package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/stretchr/testify/require"
)

// Fylvo: new installations use fylvo.db, installations from before the rename keep cloudreve.db.
func TestDefaultDBFile(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(old) })
	util.UseWorkingDir = true
	t.Cleanup(func() { util.UseWorkingDir = false })

	touch := func(name string) { require.NoError(t, os.WriteFile(filepath.Join("data", name), nil, 0600)) }

	// New installation.
	require.Equal(t, filepath.Join("data", "fylvo.db"), defaultDBFile())

	// Installation from before the rename.
	touch("cloudreve.db")
	require.Equal(t, filepath.Join("data", "cloudreve.db"), defaultDBFile())

	// Both: fylvo.db wins.
	touch("fylvo.db")
	require.Equal(t, filepath.Join("data", "fylvo.db"), defaultDBFile())
}
