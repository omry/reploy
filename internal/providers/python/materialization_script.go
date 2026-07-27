package python

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const materializationScriptLogicalPath = "scripts/python-materialize-v1.sh"

const materializationScriptV1 = `#!/bin/sh
set -eu

interpreter=$1
venv_python=$2
venv_root=$3
shift 3

"$interpreter" -m venv "$venv_root"
"$venv_python" -m pip --disable-pip-version-check install --no-index --no-deps --no-cache-dir "$@"
`

func publishMaterializationScript(ctx context.Context, sink providerapi.ArtifactSink) (providerstore.ArtifactDescriptor, error) {
	descriptor, err := sink.Publish(ctx, materializationScriptLogicalPath, providerapi.BuildMountSourceScript, strings.NewReader(materializationScriptV1))
	if err != nil {
		return providerstore.ArtifactDescriptor{}, fmt.Errorf("publish Python materialization script: %w", err)
	}
	expected := materializationScriptDescriptor()
	if err := descriptor.Validate(); err != nil {
		return providerstore.ArtifactDescriptor{}, fmt.Errorf("published Python materialization script descriptor: %w", err)
	}
	if descriptor != expected {
		return providerstore.ArtifactDescriptor{}, fmt.Errorf("published Python materialization script descriptor does not match provider script")
	}
	return descriptor, nil
}

func materializationScriptDescriptor() providerstore.ArtifactDescriptor {
	digest := sha256.Sum256([]byte(materializationScriptV1))
	return providerstore.ArtifactDescriptor{
		LogicalPath: materializationScriptLogicalPath,
		Kind:        providerapi.BuildMountSourceScript,
		Size:        strconv.Itoa(len(materializationScriptV1)),
		SHA256:      canonical.Digest(fmt.Sprintf("sha256:%x", digest)),
	}
}
