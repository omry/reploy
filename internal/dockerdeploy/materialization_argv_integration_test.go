package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/providers"
)

func TestMaterializationArgvChildEnvironmentDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}

	base := "debian:bookworm-slim"
	runDockerIntegration(t, context.Background(), "pull", base)

	scriptDirectory := t.TempDir()
	script := `#!/bin/sh
set -eu
test "${EXPECTED-}" = "literal value"
test -z "${LEAKED+x}"
test "$(umask)" = "0022"
test "$(readlink /proc/self/fd/0)" = "/dev/null"
touch /tmp/reploy-umask-evidence
test "$(stat -c %a /tmp/reploy-umask-evidence)" = "644"
`
	if err := os.WriteFile(filepath.Join(scriptDirectory, "check.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	transaction := rendererTransaction()
	transaction.Script.LogicalPath = "scripts/check.sh"
	transaction.Argv = []providers.TypedArgument{
		{Kind: providers.TypedArgumentValidatedExecutable, ExecutableID: "carrier"},
		{Kind: providers.TypedArgumentLiteral, Literal: "-eu"},
		{Kind: providers.TypedArgumentMountedArtifact, MountID: "script", RelativePath: "check.sh"},
	}
	transaction.ChildEnvironment.Variables = []providers.EnvironmentVariable{{Name: "EXPECTED", Value: "literal value"}}
	transaction.Mounts = transaction.Mounts[:1]
	argv, err := RenderMaterializationArgv(transaction)
	if err != nil {
		t.Fatal(err)
	}

	dockerArgs := []string{
		"run", "--rm", "--env", "LEAKED=must-not-be-inherited",
		"--mount", "type=bind,source=" + scriptDirectory + ",target=/.reploy-build/script,readonly",
		base,
	}
	dockerArgs = append(dockerArgs, argv...)
	runDockerIntegration(t, context.Background(), dockerArgs...)
}
