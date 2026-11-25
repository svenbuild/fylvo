package cache

import (
	"encoding/gob"
)

func init() {
	gob.Register(map[string]itemWithTTL{})
}

// Driver key-value cache storage
type Driver interface {
	// Set a value, ttl is the expiry time in seconds
	Set(key string, value any, ttl int) error

	// Get a value and return whether it succeeded
	Get(key string) (any, bool)

	// Get values in batch, returns a map of the found values plus the missing ones
	Gets(keys []string, prefix string) (map[string]any, []string)

	// Set values in batch, all keys get the prefix
	Sets(values map[string]any, prefix string) error

	// Delete values by [Prefix + key]. If no ket is presented, all keys with given prefix will be deleted.
	Delete(prefix string, keys ...string) error

	// Save in-memory cache to disk
	Persist(path string) error

	// Restore cache from disk
	Restore(path string) error

	// Remove all entries
	DeleteAll() error
}
