package connected

import (
	"context"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

// managers holds the Manager of each running server, by the database client of the
// server. Not by the Dep: every request gets a Dep of its own.
var managers sync.Map // *ent.Client -> *Manager

// For returns the Manager of a running server. There is one per server: sign-ins in
// progress, listings that are paged through and open connections live in its memory.
func For(dep dependency.Dep) *Manager {
	key := dep.DBClient()
	if m, ok := managers.Load(key); ok {
		return m.(*Manager)
	}

	options := ManagerOptions{
		Storages: inventory.NewConnectedStorageClient(dep.DBClient()),
		Settings: &depSettings{client: dep.SettingClient(), kv: dep.KV()},
		Sealer: NewSealer(func(ctx context.Context) ([]byte, error) {
			return dep.MasterEncryptKeyVault(ctx).GetMasterKey(ctx)
		}),
		UserExists: func(ctx context.Context, userID int) bool {
			u, err := dep.UserClient().GetByID(ctx, userID)
			return err == nil && u != nil
		},
	}
	devOptions(&options)
	actual, loaded := managers.LoadOrStore(key, NewManager(options))
	if !loaded {
		devReady(dep, actual.(*Manager))
	}
	return actual.(*Manager)
}

// Install makes m the Manager of a server. For tests that bring their own providers.
func Install(dep dependency.Dep, m *Manager) {
	managers.Store(dep.DBClient(), m)
}

// DepSettings returns the settings of a server as a Manager reads and writes them.
func DepSettings(dep dependency.Dep) SettingStore {
	return &depSettings{client: dep.SettingClient(), kv: dep.KV()}
}

// DepSealer returns the Sealer that works with the master key of a server.
func DepSealer(dep dependency.Dep) *Sealer {
	return NewSealer(func(ctx context.Context) ([]byte, error) {
		return dep.MasterEncryptKeyVault(ctx).GetMasterKey(ctx)
	})
}

// depSettings reads the settings from the database and clears the cached copies of what
// it writes, like the settings page of the admin area does.
type depSettings struct {
	client inventory.SettingClient
	kv     cache.Driver
}

func (s *depSettings) Gets(ctx context.Context, names []string) (map[string]string, error) {
	return s.client.Gets(ctx, names)
}

func (s *depSettings) Set(ctx context.Context, settings map[string]string) error {
	if err := s.client.Set(ctx, settings); err != nil {
		return err
	}
	names := make([]string, 0, len(settings))
	for name := range settings {
		names = append(names, name)
	}
	return s.kv.Delete(setting.KvSettingPrefix, names...)
}
