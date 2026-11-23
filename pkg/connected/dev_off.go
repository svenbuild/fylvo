//go:build !fylvo_dev

package connected

import (
	"net/http"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
)

// The simulated provider for development (see dev.go) is not part of this build.

// DevHandler returns nil: this build has no simulated provider.
func DevHandler() http.Handler { return nil }

func devOptions(*ManagerOptions) {}

func devReady(dependency.Dep, *Manager) {}
