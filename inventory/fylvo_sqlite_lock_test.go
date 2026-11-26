package inventory

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Fylvo: the database of a test instance stayed locked for good. A query whose context
// ended just as it had its first row was left open by the SQLite driver (fixed in
// modernc.org/sqlite v1.40.1): its connection kept its locks, and an interrupt that hit
// nothing. Every later statement on that connection was interrupted, and once the pool
// replaced it, every write of the new one was refused with "database is locked".

// openLikeServer opens a SQLite file the way NewRawEntClient does.
func openLikeServer(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	require.NoError(t, err)
	db.SetMaxIdleConns(50)
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(time.Hour)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec("CREATE TABLE a (id INTEGER PRIMARY KEY, v TEXT)")
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO a (v) WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 20000)
		SELECT hex(randomblob(16)) FROM c`)
	require.NoError(t, err)
	return db
}

func TestSQLiteQueryEndedByItsContextLeavesNoLock(t *testing.T) {
	db := openLikeServer(t)
	// Contexts that end at every moment of a query, also just as it has its first row.
	for i := 0; i < 1500; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i%40)*50*time.Microsecond)
		rows, err := db.QueryContext(ctx, "SELECT v FROM a ORDER BY v LIMIT 1")
		if err == nil {
			for rows.Next() {
			}
			rows.Close()
		}
		cancel()
	}

	write := func(what string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, "INSERT INTO a (v) VALUES (?)", what)
		require.NoError(t, err, what)
	}
	write("on the same connection")
	// The pool replaces the connection, as it does after an hour.
	db.SetMaxIdleConns(0)
	db.SetConnMaxLifetime(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	write("on a new connection")
}

// Every connection waits for a lock held by someone else instead of failing at once.
func TestSQLiteConnectionsWaitForALock(t *testing.T) {
	db := openLikeServer(t)
	var timeout int
	require.NoError(t, db.QueryRow("PRAGMA busy_timeout").Scan(&timeout))
	require.Equal(t, sqliteBusyTimeout, timeout)
	// The other setting of every connection, run in the same statement.
	var foreignKeys int
	require.NoError(t, db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys))
	require.Equal(t, 1, foreignKeys)
}

// A new database is written with the write-ahead log, synced at every commit.
func TestSQLiteUsesTheWriteAheadLog(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	var mode string
	require.NoError(t, db.QueryRow("PRAGMA journal_mode").Scan(&mode))
	require.Equal(t, "wal", mode)
	var synchronous int
	require.NoError(t, db.QueryRow("PRAGMA synchronous").Scan(&synchronous))
	require.Equal(t, 2, synchronous)
}
