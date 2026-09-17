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

const materializationScriptLogicalPath = "scripts/python-materialize-v3.sh"

const materializationScriptV1 = `#!/bin/sh
set -eu

interpreter=$1
venv_python=$2
venv_root=$3
shift 3

reject_unsafe_venv_root() {
    root=$1
    case "$root" in
        /*) ;;
        *)
            echo "Python runtime root must be absolute: $root" >&2
            return 1
            ;;
    esac
    if [ "$root" = "/" ]; then
        echo "Python runtime root cannot be the filesystem root" >&2
        return 1
    fi
    if [ -e "$root" ] || [ -L "$root" ]; then
        echo "Python runtime root already exists: $root" >&2
        return 1
    fi

    ancestor=$root
    while [ "$ancestor" != "/" ]; do
        ancestor=${ancestor%/*}
        if [ -z "$ancestor" ]; then
            ancestor=/
        fi
        if [ -L "$ancestor" ]; then
            echo "Python runtime root has a symlinked ancestor: $ancestor" >&2
            return 1
        fi
        if [ -e "$ancestor" ] && [ ! -d "$ancestor" ]; then
            echo "Python runtime root has a non-directory ancestor: $ancestor" >&2
            return 1
        fi
    done
}

reject_unsafe_venv_root "$venv_root"
"$interpreter" -m venv "$venv_root"

validate_python_outputs() {
    if [ "$#" -eq 0 ]; then
        echo "Python materialization is missing its wheel marker" >&2
        return 1
    fi
    if [ "$1" = "--reploy-python-wheel" ]; then
        shift
        if [ "$#" -eq 0 ]; then
            echo "Python materialization requires at least one wheel" >&2
            return 1
        fi
        # The wheel arguments are the original positional arguments after the
        # fixed marker. They are never joined, split, or evaluated as shell.
        "$venv_python" -m pip --disable-pip-version-check install --no-index --no-deps --no-cache-dir "$@"
        return $?
    fi

    output=$1
    shift
    # Run the remainder in a subshell so each recursive frame retains its own
    # output path while the innermost frame invokes pip with untouched wheel
    # argv. This keeps exact filenames intact.
    if ! (validate_python_outputs "$@"); then
        return 1
    fi
    case "$output" in
        "$venv_root"/*) ;;
        *)
            echo "Python console script is outside the application runtime root: $output" >&2
            return 1
            ;;
    esac
    if [ ! -f "$output" ] || [ -L "$output" ] || [ ! -x "$output" ]; then
        echo "Python console script was not generated as an executable regular file: $output" >&2
        return 1
    fi
    shebang=""
    IFS= read -r shebang < "$output" || :
    if [ "$shebang" != "#!$venv_python" ]; then
        echo "Python console script does not name the application interpreter: $output" >&2
        return 1
    fi
}

if [ "$#" -eq 0 ] || [ "$1" != "--reploy-python-output" ]; then
    echo "Python materialization is missing its output marker" >&2
    exit 1
fi
shift
validate_python_outputs "$@"
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
