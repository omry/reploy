//go:build linux || darwin

package dockerdeploy

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestValidateRuntimeHostSourcesV1PreservesExplicitDirectoryWithNestedSpecialObject(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "service.pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
		Destination: "/mnt/project", SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true,
	}})
	if err := ValidateRuntimeHostSourcesV1(policy, "command/check", 1000, []RuntimeHostSourceV1{{
		Destination: "/mnt/project", HostPath: root, SourceKind: deploy.RuntimeMountSourceDirectory,
		Authority: runtimeHostAuthorityInputV1, ReadOnly: true,
	}}); err != nil {
		t.Fatal(err)
	}
}
