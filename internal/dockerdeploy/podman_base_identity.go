package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

type registryManifestDescriptor struct {
	MediaType string           `json:"mediaType"`
	Digest    canonical.Digest `json:"digest"`
	Size      int64            `json:"size"`
	Platform  struct {
		OS, Architecture, Variant string
	} `json:"platform"`
}

type registryManifestObservation struct {
	Ref        string                     `json:"Ref"`
	Descriptor registryManifestDescriptor `json:"Descriptor"`
	Raw        []byte                     `json:"Raw"`
}

// Podman 6.1.4's compatibility pull stream omits Docker's Digest line (also
// observed on 6.2.0-dev). Use verified registry content, not a RepoDigests guess.
// A future engine that supplies Digest never enters this compatibility path.
func resolvePodmanBaseIdentity(ctx context.Context, author string, platform blueprint.Platform, run dockerOutputRunner) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	version, err := run(ctx, "version", "--format", "{{json .}}")
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("identify pull engine: %w", err)
	}
	engine, err := parseGeneratedImageBuilder(version)
	if err != nil || engine.mode != generatedPodmanAPI {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("missing pull digest requires an identified Podman engine")
	}
	// Docker's registry client retains its existing credential and TLS handling.
	output, err := run(ctx, "manifest", "inspect", "--verbose", author)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("Podman %s registry manifest evidence: %w", engine.serverVersion, err)
	}
	manifest, configID, err := podmanRegistryBaseIdentity(author, platform, []byte(output))
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	// Inspect the full content ID before touching the mutable tag. The manifest
	// binds this config to the author repository and selected platform.
	output, err = run(ctx, "image", "inspect", string(configID))
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("inspect registry-bound immutable base: %w", err)
	}
	descriptor, config, err := parseResolvedDockerBase(author, platform, []byte(output), manifest)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	if descriptor.ConfigDigest != configID {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("registry manifest config differs from immutable local image")
	}
	output, err = run(ctx, "image", "inspect", author)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("check pulled base tag: %w", err)
	}
	current, currentConfig, err := parseResolvedDockerBase(author, platform, []byte(output), manifest)
	if err != nil || !reflect.DeepEqual(current, descriptor) || !reflect.DeepEqual(currentConfig, config) {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("pulled base reference changed or differs from registry-bound immutable content")
	}
	return descriptor, config, nil
}

func podmanRegistryBaseIdentity(author string, platform blueprint.Platform, data []byte) (canonical.Digest, canonical.Digest, error) {
	if len(data) == 0 || len(data) > 8<<20 {
		return "", "", fmt.Errorf("registry manifest evidence is empty or exceeds 8 MiB")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return "", "", fmt.Errorf("registry manifest evidence is empty")
	}
	var records []registryManifestObservation
	indexed := data[0] == '['
	if indexed {
		if err := json.Unmarshal(data, &records); err != nil {
			return "", "", fmt.Errorf("decode registry manifest index evidence: %w", err)
		}
	} else {
		var record registryManifestObservation
		if err := json.Unmarshal(data, &record); err != nil {
			return "", "", fmt.Errorf("decode registry manifest evidence: %w", err)
		}
		records = []registryManifestObservation{record}
	}
	if len(records) == 0 || len(records) > 128 {
		return "", "", fmt.Errorf("registry manifest evidence requires 1 to 128 records")
	}
	var selected *registryManifestObservation
	for i := range records {
		record := &records[i]
		ref, digest, found := strings.Cut(record.Ref, "@")
		// Single manifests retain the requested tag in Docker CLI's Ref. Their
		// descriptor and Raw bytes bind content below; index children must be pinned.
		if !found && !indexed {
			digest, found = string(record.Descriptor.Digest), true
		}
		if !found || canonicalDockerRepository(dockerRepositoryWithoutTag(ref)) != canonicalDockerRepository(author) || digest != string(record.Descriptor.Digest) || record.Descriptor.Digest.Validate() != nil {
			return "", "", fmt.Errorf("registry manifest descriptor is not bound to the author repository")
		}
		p := record.Descriptor.Platform
		if indexed && (p.OS == "" || p.Architecture == "") {
			return "", "", fmt.Errorf("registry index child lacks its platform")
		}
		if p.OS != "" || p.Architecture != "" || p.Variant != "" {
			if p.OS != platform.OS || p.Architecture != platform.Architecture || platform.Variant != "" && p.Variant != platform.Variant {
				continue
			}
		}
		if selected != nil {
			return "", "", fmt.Errorf("registry manifest evidence contains conflicting selected-platform records")
		}
		selected = record
	}
	if selected == nil {
		return "", "", fmt.Errorf("registry manifest evidence has no selected platform %s", platform.Canonical)
	}
	if !indexed {
		if requested, pinned := dockerPinnedManifestDigest(author); pinned && requested != selected.Descriptor.Digest {
			return "", "", fmt.Errorf("registry manifest differs from the pinned author digest")
		}
	}
	sum := sha256.Sum256(selected.Raw)
	if selected.Descriptor.Size != int64(len(selected.Raw)) || selected.Descriptor.Digest != canonical.Digest("sha256:"+hex.EncodeToString(sum[:])) {
		return "", "", fmt.Errorf("registry raw manifest does not match its descriptor size and digest")
	}
	var manifest struct {
		SchemaVersion int                        `json:"schemaVersion"`
		MediaType     string                     `json:"mediaType"`
		Config        registryManifestDescriptor `json:"config"`
	}
	if err := json.Unmarshal(selected.Raw, &manifest); err != nil {
		return "", "", fmt.Errorf("decode registry raw image manifest: %w", err)
	}
	if manifest.SchemaVersion != 2 || manifest.MediaType != selected.Descriptor.MediaType ||
		(manifest.MediaType != "application/vnd.oci.image.manifest.v1+json" && manifest.MediaType != "application/vnd.docker.distribution.manifest.v2+json") ||
		(manifest.Config.MediaType != "application/vnd.oci.image.config.v1+json" && manifest.Config.MediaType != "application/vnd.docker.container.image.v1+json") ||
		manifest.Config.Size <= 0 || manifest.Config.Digest.Validate() != nil {
		return "", "", fmt.Errorf("registry raw image manifest lacks a valid immutable config")
	}
	return selected.Descriptor.Digest, manifest.Config.Digest, nil
}
