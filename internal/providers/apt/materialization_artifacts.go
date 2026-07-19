package apt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	materializationScriptLogicalPathV1 = "scripts/apt-materialize-v1.sh"
	materializationManifestPathV1      = "manifests/apt-state-v1.tsv"
	materializationManifestKindV1      = "apt-state-manifest"
)

const materializationScriptV1 = `#!/bin/sh
set -eu

fail() {
	code=$1
	message=$2
	printf 'reploy APT materialization failed (%s): %s\n' "$code" "$message" >&2
	exit 1
}

# The transaction builder supplies only validated executable paths and
# separately typed bundle artifacts. The closed child environment supplies
# APT_CONFIG and TMPDIR; materialization itself always has no network.
apt_get=$1
dpkg=$2
dpkg_query=$3
sha256sum=$4
awk=$5
cmp=$6
mkdir=$7
rm=$8
sort=$9
wc=${10}
manifest=${11}
shift 11

scratch=$TMPDIR
before=$scratch/before.tsv
after=$scratch/after.tsv
expected=$scratch/expected.tsv
raw=$scratch/raw.tsv
unsorted=$scratch/unsorted.tsv
artifacts=$scratch/artifacts.tsv
archives=$scratch/archives

"$mkdir" -m 0700 "$scratch" || fail materialization.failed "cannot create the private APT scratch directory"
"$mkdir" -m 0700 "$archives" || fail materialization.failed "cannot create the private APT archive directory"
printf '%s\n' 'Acquire::Languages "none";' 'Dpkg::Use-Pty "0";' >"$APT_CONFIG"

"$dpkg" --audit || fail validation.mismatch "the base image has an inconsistent dpkg state"
"$dpkg_query" --show '--showformat=${binary:Package}\t${Version}\t${Architecture}\t${Status}\n' >"$raw" || fail validation.mismatch "cannot read the base package state"
"$awk" -F '\t' -v OFS='\t' '{ sub(/:[^:]+$/, "", $1); print }' "$raw" >"$unsorted"
"$sort" "$unsorted" >"$before"

"$awk" -F '\t' 'NR == 1 { next } $1 == "bundle" { print $7 "\t" $8 "\t" $9 }' "$manifest" >"$artifacts"
while IFS="$(printf '\t')" read -r logical size digest; do
	archive=
	for candidate do
		case "$candidate" in
			*/"$logical") archive=$candidate; break ;;
		esac
	done
	if [ -z "$archive" ] || [ ! -f "$archive" ] || [ -L "$archive" ]; then
		fail apt.artifact_invalid "locked archive $logical is missing or unsafe"
	fi
	actual_size=$("$wc" -c <"$archive")
	if [ "$actual_size" != "$size" ]; then
		fail apt.artifact_invalid "locked archive $logical has the wrong size"
	fi
	actual_digest=$("$sha256sum" "$archive")
	actual_digest=${actual_digest%% *}
	if [ "sha256:$actual_digest" != "$digest" ]; then
		fail apt.artifact_invalid "locked archive $logical has the wrong digest"
	fi
done <"$artifacts"

if [ "$#" -ne 0 ]; then
	if ! "$apt_get" -o Dpkg::Use-Pty=0 -o "Dir::Cache::archives=$archives" \
		--assume-yes --no-remove --no-install-recommends \
		-o APT::Install-Suggests=false \
		-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold \
		install "$@"; then
		fail materialization.failed "the offline APT installation transaction failed"
	fi
fi

"$dpkg" --audit || fail validation.mismatch "dpkg reported an inconsistent installed state"
"$apt_get" -o Dpkg::Use-Pty=0 -o "Dir::Cache::archives=$archives" check || fail validation.mismatch "APT dependency validation failed after installation"
"$dpkg_query" --show '--showformat=${binary:Package}\t${Version}\t${Architecture}\t${Status}\n' >"$raw" || fail validation.mismatch "cannot read the installed package state"
"$awk" -F '\t' -v OFS='\t' '{ sub(/:[^:]+$/, "", $1); print }' "$raw" >"$unsorted"
"$sort" "$unsorted" >"$after"

"$awk" -F '\t' -v OFS='\t' '
	FNR == NR {
		if (FNR == 1) next
		kind[$2]=$1; version[$2]=$3; arch[$2]=$4; predecessor[$2]=$5
		next
	}
	{
		name=$1; sub(/:[^:]+$/, "", name)
		seen[name]=1
		if (kind[name] == "base") {
			if ($2 != version[name] || $3 != arch[name] || $4 != "install ok installed") exit 21
		} else if (kind[name] == "bundle") {
			if (predecessor[name] == "-") exit 22
			if ($2 != predecessor[name]) exit 23
			$1=name; $2=version[name]; $3=arch[name]; $4="install ok installed"
		}
		print
	}
	END {
		for (name in kind) {
			if (!seen[name]) {
				if (kind[name] != "bundle" || predecessor[name] != "-") exit 24
				print name, version[name], arch[name], "install ok installed"
			}
		}
	}
' "$manifest" "$before" >"$unsorted" || fail validation.mismatch "the base package state does not match the resolved APT bundle"
"$sort" "$unsorted" >"$expected"

"$cmp" "$expected" "$after" || fail validation.mismatch "the installed package state differs from the resolved APT bundle"
"$rm" -rf "$scratch" || fail materialization.failed "cannot remove the private APT scratch directory"
`

func materializationScriptDescriptorV1() providerstore.ArtifactDescriptor {
	return materializationArtifactDescriptorV1(
		materializationScriptLogicalPathV1,
		providers.BuildMountSourceScript,
		[]byte(materializationScriptV1),
	)
}

func materializationStateManifestDescriptorV1(content []byte) providerstore.ArtifactDescriptor {
	return materializationArtifactDescriptorV1(materializationManifestPathV1, materializationManifestKindV1, content)
}

func materializationArtifactDescriptorV1(logicalPath string, kind string, content []byte) providerstore.ArtifactDescriptor {
	digest := sha256.Sum256(content)
	return providerstore.ArtifactDescriptor{
		LogicalPath: logicalPath,
		Kind:        kind,
		Size:        strconv.Itoa(len(content)),
		SHA256:      canonical.Digest(fmt.Sprintf("sha256:%x", digest)),
	}
}

func materializationStateManifestBytesV1(bundle BundleV1) ([]byte, error) {
	if !validResolverArchitectureV1(bundle.NativeArchitecture) {
		return nil, fmt.Errorf("APT materialization manifest native architecture is invalid")
	}
	var output strings.Builder
	output.WriteString("reploy-apt-state-v1\t")
	output.WriteString(bundle.NativeArchitecture)
	output.WriteByte('\n')
	for _, pkg := range bundle.BasePackages {
		fmt.Fprintf(&output, "base\t%s\t%s\t%s\t-\t-\t-\t-\t-\n", pkg.Tuple.Name, pkg.Tuple.Version, pkg.Tuple.Architecture)
	}
	for _, pkg := range bundle.BundlePackages {
		predecessor := "-"
		if pkg.BasePredecessor != nil {
			predecessor = pkg.BasePredecessor.Version
		}
		fmt.Fprintf(
			&output,
			"bundle\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			pkg.Tuple.Name,
			pkg.Tuple.Version,
			pkg.Tuple.Architecture,
			predecessor,
			pkg.FileListDigest,
			pkg.Artifact.LogicalPath,
			pkg.Artifact.Size,
			pkg.Artifact.SHA256,
		)
	}
	return []byte(output.String()), nil
}

// PublishMaterializationArtifactsV1 publishes the fixed recipe and the exact
// closure-derived state manifest, rejecting any sink that returns a different
// descriptor.
func PublishMaterializationArtifactsV1(ctx context.Context, sink providers.ArtifactSink, bundle BundleV1) error {
	if err := ValidateBundleV1(bundle); err != nil {
		return err
	}
	manifest, err := materializationStateManifestBytesV1(bundle)
	if err != nil {
		return err
	}
	for _, artifact := range []struct {
		descriptor providerstore.ArtifactDescriptor
		content    []byte
	}{
		{bundle.Script, []byte(materializationScriptV1)},
		{bundle.StateManifest, manifest},
	} {
		actual, err := sink.Publish(ctx, artifact.descriptor.LogicalPath, artifact.descriptor.Kind, bytes.NewReader(artifact.content))
		if err != nil {
			return fmt.Errorf("publish APT materialization artifact %q: %w", artifact.descriptor.LogicalPath, err)
		}
		if actual != artifact.descriptor {
			return fmt.Errorf("published APT materialization artifact %q does not match provider content", artifact.descriptor.LogicalPath)
		}
	}
	return nil
}
