package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/task"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/queue"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

const (
	archivePassword = "archive-secret-1"
	sharePassword   = "share-secret-2"
	shareSrc        = "cloudreve://Ab3d:" + sharePassword + "@share/a.zip"
	shareDst        = "cloudreve://Xy9z:" + sharePassword + "@share/out"
)

// archiveSecretsEnv is a database with a master key and the dependencies in a context, as a
// queue worker has them.
func archiveSecretsEnv(t *testing.T) (context.Context, *ent.Client) {
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := inventory.InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	hasher, err := hashid.New("fylvo-test")
	require.NoError(t, err)

	dep := dependency.NewDependency(
		dependency.WithLogger(logger),
		dependency.WithConfigPath(filepath.Join(t.TempDir(), "conf.ini")),
		dependency.WithDbClient(client),
		dependency.WithKV(kv),
		dependency.WithHashIDEncoder(hasher),
	)
	ctx := context.WithValue(context.Background(), dependency.DepCtx{}, dep)
	owner, err := client.User.Create().SetEmail("owner@fylvo.test").SetNick("owner").
		SetStatus(user.StatusActive).SetGroupID(2).SetSettings(&types.UserSetting{}).Save(ctx)
	require.NoError(t, err)
	return context.WithValue(ctx, inventory.UserCtx{}, owner), client
}

func requireNoSecret(t *testing.T, text string) {
	t.Helper()
	require.NotContains(t, text, archivePassword)
	require.NotContains(t, text, sharePassword)
}

func TestExtractTaskKeepsNoPasswordReadable(t *testing.T) {
	ctx, _ := archiveSecretsEnv(t)

	created, err := NewExtractArchiveTask(ctx, shareSrc, shareDst, "", archivePassword, nil)
	require.NoError(t, err)
	extract := created.(*ExtractArchiveTask)
	stored := extract.State()
	requireNoSecret(t, stored)

	// The worker gets the passwords back, the summary for the API shows none.
	state := &ExtractArchiveTaskState{}
	require.NoError(t, json.Unmarshal([]byte(stored), state))
	require.Equal(t, "cloudreve://Ab3d@share/a.zip", state.Uri)
	secrets, err := loadArchiveSecrets(ctx, &state.Secrets, &state.Password, &state.Uri, &state.Dst)
	require.NoError(t, err)
	require.Equal(t, archivePassword, secrets.Password)
	require.Equal(t, shareSrc, secrets.uri(state.Uri))
	require.Equal(t, shareDst, secrets.uri(state.Dst))
	summary, err := json.Marshal(extract.Summarize(nil))
	require.NoError(t, err)
	requireNoSecret(t, string(summary))

	// Sealed for one owner, the secrets do not open for another.
	other := context.WithValue(ctx, inventory.UserCtx{}, &ent.User{ID: 999})
	_, err = loadArchiveSecrets(other, &state.Secrets, &state.Password, &state.Uri, &state.Dst)
	require.Error(t, err)

	// A finished task forgets them.
	extract.state = state
	require.NoError(t, extract.Cleanup(ctx))
	require.NotContains(t, extract.State(), "secrets")
}

func TestExtractTaskOfAnOlderVersionStillRuns(t *testing.T) {
	ctx, _ := archiveSecretsEnv(t)
	legacy, err := json.Marshal(&ExtractArchiveTaskState{Uri: shareSrc, Dst: shareDst, Password: archivePassword})
	require.NoError(t, err)

	state := &ExtractArchiveTaskState{}
	require.NoError(t, json.Unmarshal(legacy, state))
	secrets, err := loadArchiveSecrets(ctx, &state.Secrets, &state.Password, &state.Uri, &state.Dst)
	require.NoError(t, err)
	require.Equal(t, archivePassword, secrets.Password)
	require.Equal(t, shareSrc, secrets.uri(state.Uri))
	resaved, err := json.Marshal(state)
	require.NoError(t, err)
	requireNoSecret(t, string(resaved))
}

func TestCreateArchiveTaskKeepsNoPasswordReadable(t *testing.T) {
	ctx, _ := archiveSecretsEnv(t)
	created, err := NewCreateArchiveTask(ctx, []string{shareSrc, "cloudreve://my/b.txt"}, shareDst+"/x.zip")
	require.NoError(t, err)
	requireNoSecret(t, created.State())

	state := &CreateArchiveTaskState{}
	require.NoError(t, json.Unmarshal([]byte(created.State()), state))
	secrets, err := loadArchiveSecrets(ctx, &state.Secrets, nil, state.uriFields()...)
	require.NoError(t, err)
	require.Equal(t, []string{shareSrc, "cloudreve://my/b.txt"}, secrets.uris(state.Uris))
	require.Equal(t, shareDst+"/x.zip", secrets.uri(state.Dst))
}

func TestFinishedArchiveTasksForgetTheirPasswords(t *testing.T) {
	ctx, client := archiveSecretsEnv(t)
	owner := inventory.UserFromContext(ctx)
	add := func(taskType string, status task.Status, state any) *ent.Task {
		raw, err := json.Marshal(state)
		require.NoError(t, err)
		created, err := client.Task.Create().SetType(taskType).SetStatus(status).SetPrivateState(string(raw)).
			SetPublicState(&types.TaskPublicState{}).SetUserID(owner.ID).SetCorrelationID(uuid.Must(uuid.NewV4())).Save(ctx)
		require.NoError(t, err)
		return created
	}
	finished := add(queue.ExtractArchiveTaskType, task.StatusCompleted,
		&ExtractArchiveTaskState{Uri: shareSrc, Dst: shareDst, Password: archivePassword})
	failed := add(queue.CreateArchiveTaskType, task.StatusError,
		&CreateArchiveTaskState{Uris: []string{shareSrc}, Dst: shareDst})
	running := add(queue.ExtractArchiveTaskType, task.StatusSuspending,
		&ExtractArchiveTaskState{Uri: shareSrc, Password: archivePassword})

	require.NoError(t, ForgetArchiveSecrets(ctx, client))

	for _, cleared := range []*ent.Task{finished, failed} {
		reloaded, err := client.Task.Get(ctx, cleared.ID)
		require.NoError(t, err)
		requireNoSecret(t, reloaded.PrivateState)
	}
	// A task that still runs keeps them; it seals them when it runs next.
	reloaded, err := client.Task.Get(ctx, running.ID)
	require.NoError(t, err)
	require.Equal(t, running.PrivateState, reloaded.PrivateState)
}

func TestTaskErrorsHideSharePasswords(t *testing.T) {
	failing := &queue.DBTask{Task: &ent.Task{PublicState: &types.TaskPublicState{}}}
	failing.OnRetry(errors.New("failed to get archive file " + shareSrc))
	failing.OnError(errors.New("failed to parse dst uri "+shareDst), 0)
	public, err := json.Marshal(failing.Task.PublicState)
	require.NoError(t, err)
	requireNoSecret(t, string(public))
}
