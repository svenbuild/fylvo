package webdav

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
)

type copyMoveOperationsStub struct {
	calls      []string
	deleteErr  error
	moveErr    error
	renameErr  error
	moveIsCopy bool
	// copyName is the name WithCopyName gave the last copy.
	copyName string
	// firstMoveErr is returned by the first move only.
	firstMoveErr error
}

func (s *copyMoveOperationsStub) Delete(context.Context, []*fs.URI, ...fs.Option) error {
	s.calls = append(s.calls, "delete")
	return s.deleteErr
}

func (s *copyMoveOperationsStub) MoveOrCopy(_ context.Context, _ []*fs.URI, _ *fs.URI, isCopy bool, opts ...fs.Option) error {
	s.calls = append(s.calls, "move")
	s.moveIsCopy = isCopy
	o := &fs.FsOption{}
	for _, opt := range opts {
		opt.Apply(o)
	}
	s.copyName = o.CopyName
	if err := s.firstMoveErr; err != nil {
		s.firstMoveErr = nil
		return err
	}
	return s.moveErr
}

func (s *copyMoveOperationsStub) Rename(context.Context, *fs.URI, string) (fs.File, error) {
	s.calls = append(s.calls, "rename")
	return nil, s.renameErr
}

func TestParseOverwrite(t *testing.T) {
	tests := []struct {
		value string
		want  bool
		err   bool
	}{
		{value: "", want: true},
		{value: "T", want: true},
		{value: "F", want: false},
		{value: "true", err: true},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := parseOverwrite(test.value)
			if (err != nil) != test.err {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("unexpected overwrite value: %v", got)
			}
		})
	}
}

func TestPerformCopyMove(t *testing.T) {
	src := mustWebDAVTestURI(t, "cloudreve://my/source.temp")
	dst := mustWebDAVTestURI(t, "cloudreve://my/source.txt")
	dstFolder := dst.DirUri()

	t.Run("overwrite disabled", func(t *testing.T) {
		operations := &copyMoveOperationsStub{}
		status, err := performCopyMove(context.Background(), operations, src, dst, dstFolder, false, false, true)
		if status != http.StatusPreconditionFailed || !errors.Is(err, errDestinationExists) {
			t.Fatalf("unexpected result: status=%d err=%v", status, err)
		}
		if len(operations.calls) != 0 {
			t.Fatalf("unexpected operations: %v", operations.calls)
		}
	})

	t.Run("overwrite existing move", func(t *testing.T) {
		operations := &copyMoveOperationsStub{}
		status, err := performCopyMove(context.Background(), operations, src, dst, dstFolder, false, true, true)
		if err != nil || status != http.StatusNoContent {
			t.Fatalf("unexpected result: status=%d err=%v", status, err)
		}
		if got := operations.calls; len(got) != 3 || got[0] != "delete" || got[1] != "move" || got[2] != "rename" {
			t.Fatalf("unexpected operation order: %v", got)
		}
		if operations.moveIsCopy {
			t.Fatal("move was executed as copy")
		}
	})

	t.Run("new copy", func(t *testing.T) {
		operations := &copyMoveOperationsStub{}
		status, err := performCopyMove(context.Background(), operations, src, dst, dstFolder, true, true, false)
		if err != nil || status != http.StatusCreated {
			t.Fatalf("unexpected result: status=%d err=%v", status, err)
		}
		// Fylvo: the copy gets its name right away, no rename after.
		if got := operations.calls; len(got) != 1 || got[0] != "move" {
			t.Fatalf("unexpected operation order: %v", got)
		}
		if !operations.moveIsCopy {
			t.Fatal("copy was executed as move")
		}
		if operations.copyName != "source.txt" {
			t.Fatalf("unexpected copy name: %q", operations.copyName)
		}
	})

	t.Run("copy under the same name", func(t *testing.T) {
		operations := &copyMoveOperationsStub{}
		other := mustWebDAVTestURI(t, "cloudreve://my/Ordner/source.temp")
		status, err := performCopyMove(context.Background(), operations, src, other, other.DirUri(), true, true, false)
		if err != nil || status != http.StatusCreated || operations.copyName != "" {
			t.Fatalf("unexpected result: status=%d err=%v name=%q", status, err, operations.copyName)
		}
	})

	// Fylvo: the destination folder holds the old name, so the move renames first.
	t.Run("move into a folder that holds the old name", func(t *testing.T) {
		other := mustWebDAVTestURI(t, "cloudreve://my/Ordner/neu.txt")
		operations := &copyMoveOperationsStub{firstMoveErr: fs.ErrFileExisted}
		status, err := performCopyMove(context.Background(), operations, src, other, other.DirUri(), false, true, false)
		if err != nil || status != http.StatusCreated {
			t.Fatalf("unexpected result: status=%d err=%v", status, err)
		}
		if got := operations.calls; len(got) != 3 || got[0] != "move" || got[1] != "rename" || got[2] != "move" {
			t.Fatalf("unexpected operation order: %v", got)
		}
	})
}

func mustWebDAVTestURI(t *testing.T, raw string) *fs.URI {
	t.Helper()
	uri, err := fs.NewUriFromString(raw)
	if err != nil {
		t.Fatal(err)
	}
	return uri
}
