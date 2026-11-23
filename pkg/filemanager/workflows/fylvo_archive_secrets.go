package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/task"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/queue"
)

// Fylvo: the archive tasks keep no password readable in the database. The password of an
// encrypted archive and the passwords in the addresses of protected share links are sealed
// with the master key of the server (field "secrets" of the state); the addresses are stored
// without them. A finished task forgets them (Cleanup), and ForgetArchiveSecrets clears those
// of tasks that finished before.

// archiveSecrets are the passwords an archive task needs while it runs.
type archiveSecrets struct {
	Password string `json:"password,omitempty"`
	// Shares maps an address as stored (without password) to the password it came with.
	Shares map[string]string `json:"shares,omitempty"`
}

// loadArchiveSecrets opens sealed and moves the passwords still found in password and uris
// (a new task, or one stored by an older version) into the secrets, sealing them again.
// Afterwards the fields hold no password.
func loadArchiveSecrets(ctx context.Context, sealed *[]byte, password *string, uris ...*string) (*archiveSecrets, error) {
	secrets := &archiveSecrets{Shares: map[string]string{}}
	sealer := connected.DepSealer(dependency.FromContext(ctx))
	binding := archiveSecretsBinding(ctx)
	if len(*sealed) > 0 {
		plain, err := sealer.Open(ctx, connected.PurposeArchiveTask, binding, *sealed)
		if err != nil {
			return nil, fmt.Errorf("failed to open the passwords of the task: %w", err)
		}
		if err := json.Unmarshal(plain, secrets); err != nil {
			return nil, fmt.Errorf("failed to read the passwords of the task: %w", err)
		}
		if secrets.Shares == nil {
			secrets.Shares = map[string]string{}
		}
	}

	moved := false
	if password != nil && *password != "" {
		secrets.Password, *password, moved = *password, "", true
	}
	for _, uri := range uris {
		if clean, sharePassword := splitSharePassword(*uri); sharePassword != "" {
			secrets.Shares[clean], *uri, moved = sharePassword, clean, true
		}
	}
	if !moved {
		return secrets, nil
	}

	plain, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	if *sealed, err = sealer.Seal(ctx, connected.PurposeArchiveTask, binding, plain); err != nil {
		return nil, fmt.Errorf("failed to seal the passwords of the task: %w", err)
	}
	return secrets, nil
}

// uri returns a stored address with the password it came with.
func (s *archiveSecrets) uri(stored string) string {
	sharePassword, ok := s.Shares[stored]
	if !ok {
		return stored
	}
	u, err := url.Parse(stored)
	if err != nil || u.User == nil {
		return stored
	}
	u.User = url.UserPassword(u.User.Username(), sharePassword)
	return u.String()
}

func (s *archiveSecrets) uris(stored []string) []string {
	res := make([]string, len(stored))
	for i, raw := range stored {
		res[i] = s.uri(raw)
	}
	return res
}

// archiveSecretsBinding binds sealed secrets to the owner of the task.
func archiveSecretsBinding(ctx context.Context) []byte {
	id := 0
	if u := inventory.UserFromContext(ctx); u != nil {
		id = u.ID
	}
	return connected.Context("archive task", strconv.Itoa(id))
}

// splitSharePassword returns raw without the password of its user part, and that password.
func splitSharePassword(raw string) (string, string) {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw, ""
	}
	sharePassword, ok := u.User.Password()
	if !ok {
		return raw, ""
	}
	u.User = url.User(u.User.Username())
	return u.String(), sharePassword
}

// withoutSharePassword returns raw without the password of its user part.
func withoutSharePassword(raw string) string {
	clean, _ := splitSharePassword(raw)
	return clean
}

func withoutSharePasswords(raws []string) []string {
	res := make([]string, len(raws))
	for i, raw := range raws {
		res[i] = withoutSharePassword(raw)
	}
	return res
}

// uriFields returns the addresses of the task, to hide their passwords.
func (s *CreateArchiveTaskState) uriFields() []*string {
	fields := []*string{&s.Dst}
	for i := range s.Uris {
		fields = append(fields, &s.Uris[i])
	}
	return fields
}

// storeState writes state as the stored state of t.
func storeState(t *queue.DBTask, state any) {
	res, err := json.Marshal(state)
	if err != nil {
		return
	}
	t.Lock()
	t.Task.PrivateState = string(res)
	t.Unlock()
}

// forgetSecrets clears every password from the state of an archive task.
func forgetSecrets(sealed *[]byte, password *string, uris ...*string) {
	*sealed = nil
	if password != nil {
		*password = ""
	}
	for _, uri := range uris {
		*uri = withoutSharePassword(*uri)
	}
}

// ForgetArchiveSecrets clears the passwords from the stored state of archive tasks that
// finished before tasks forgot them at their end.
func ForgetArchiveSecrets(ctx context.Context, client *ent.Client) error {
	tasks, err := client.Task.Query().Where(
		task.TypeIn(queue.ExtractArchiveTaskType, queue.CreateArchiveTaskType),
		task.StatusIn(task.StatusCompleted, task.StatusError, task.StatusCanceled),
	).Select(task.FieldID, task.FieldType, task.FieldPrivateState).All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list archive tasks: %w", err)
	}

	for _, t := range tasks {
		var cleared any
		switch t.Type {
		case queue.ExtractArchiveTaskType:
			state := &ExtractArchiveTaskState{}
			if json.Unmarshal([]byte(t.PrivateState), state) != nil {
				continue
			}
			forgetSecrets(&state.Secrets, &state.Password, &state.Uri, &state.Dst)
			cleared = state
		default:
			state := &CreateArchiveTaskState{}
			if json.Unmarshal([]byte(t.PrivateState), state) != nil {
				continue
			}
			forgetSecrets(&state.Secrets, nil, state.uriFields()...)
			cleared = state
		}

		res, err := json.Marshal(cleared)
		if err != nil {
			return err
		}
		if string(res) == t.PrivateState {
			continue
		}
		if err := client.Task.Update().Where(task.ID(t.ID)).SetPrivateState(string(res)).Exec(ctx); err != nil {
			return fmt.Errorf("failed to clear the passwords of task %d: %w", t.ID, err)
		}
	}
	return nil
}
