// Package connectedtest helps the tests of connected storages.
package connectedtest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ExamplesDir names the environment variable with the folder the examples are written
// to. Without it the examples are only built.
const ExamplesDir = "FYLVO_CONNECTED_EXAMPLES"

// Examples collects answers of the API the way a client receives them, for the people
// who write clients: run the tests with ExamplesDir set and read the files.
type Examples struct {
	t   *testing.T
	dir string
}

// NewExamples starts a collection. Answers are built as a server in production builds
// them, without the details only a development server adds to an error.
func NewExamples(t *testing.T) *Examples {
	mode := gin.Mode()
	gin.SetMode(gin.ReleaseMode)
	t.Cleanup(func() { gin.SetMode(mode) })

	dir := os.Getenv(ExamplesDir)
	if dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	return &Examples{t: t, dir: dir}
}

// Add notes the answer of a route as the example with the given name: data, or the
// answer for err when there is one.
func (x *Examples) Add(name string, data any, err error) {
	res := serializer.Response{Data: data}
	if err != nil {
		res = serializer.Err(context.Background(), err)
	}
	text, marshalErr := json.MarshalIndent(res, "", "  ")
	require.NoError(x.t, marshalErr, name)

	if x.dir != "" {
		require.NoError(x.t, os.WriteFile(filepath.Join(x.dir, name+".json"), append(text, '\n'), 0o644))
	}
}
