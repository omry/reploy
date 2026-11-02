package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type preparedImageEvidenceV1 struct {
	Identity     deploy.ImageIdentityEvidenceV1
	Destinations *deploy.ImageDestinationEvidenceV1
}

// Use the selected generated builder only. No tag, inspect, pull or image execution.
func exportDockerPreparedImageV1(ctx context.Context, document blueprint.Document, image providers.RealizedImageV1, platform blueprint.Platform) (preparedImageEvidenceV1, error) {
	policy := document.Environment.Runtime.IdentityPolicy
	builder := currentGeneratedImageBuilder(ctx)
	if builder == nil || image.Validate() != nil || policy == nil || policy.Validate() != nil || platform.Validate() != nil || platform.OS != "linux" {
		return preparedImageEvidenceV1{}, invalidImageIdentityArchiveV1()
	}
	var evidence preparedImageEvidenceV1
	err := streamDockerImageArchiveV1(ctx, builder.endpoint, string(image.ConfigDigest), func(ctx context.Context, input io.Reader) error {
		var err error
		evidence, err = readDockerPreparedImageArchiveV1(ctx, input, document, image, platform)
		return err
	})
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return preparedImageEvidenceV1{}, fmt.Errorf("image identity export failed (exit %d)", exit.ExitCode())
		}
		return preparedImageEvidenceV1{}, fmt.Errorf("image identity export failed")
	}
	return evidence, nil
}

func readDockerPreparedImageArchiveV1(ctx context.Context, input io.Reader, document blueprint.Document, image providers.RealizedImageV1, platform blueprint.Platform) (preparedImageEvidenceV1, error) {
	policy := document.Environment.Runtime.IdentityPolicy
	fail := func() (preparedImageEvidenceV1, error) {
		return preparedImageEvidenceV1{}, invalidImageIdentityArchiveV1()
	}
	if image.Validate() != nil || policy == nil || policy.Validate() != nil || platform.Validate() != nil || platform.OS != "linux" {
		return fail()
	}
	selection, err := deploy.ImageDestinationSelectionDigestV1(document)
	if err != nil {
		return fail()
	}
	layers := &imageIdentityLayersV1{projectionBudget: &imageDestinationBudgetV1{}}
	archive, err := readDockerImageArchiveV1(ctx, input, string(image.ConfigDigest), layers)
	if err != nil {
		return fail()
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(archive.config, &config) != nil || rejectHealthFieldAliases(config, []string{"os", "architecture", "variant", "rootfs"}) != nil {
		return fail()
	}
	var os, architecture, variant string
	if json.Unmarshal(config["os"], &os) != nil || json.Unmarshal(config["architecture"], &architecture) != nil {
		return fail()
	}
	if raw, present := config["variant"]; present {
		if string(raw) == "null" || json.Unmarshal(raw, &variant) != nil {
			return fail()
		}
	}
	if os != "linux" || os != platform.OS || architecture != platform.Architecture || variant != platform.Variant {
		return fail()
	}
	var rootfs map[string]json.RawMessage
	if json.Unmarshal(config["rootfs"], &rootfs) != nil || rootfs == nil || rejectHealthFieldAliases(rootfs, []string{"type", "diff_ids"}) != nil {
		return fail()
	}
	var kind string
	var diffIDs []canonical.Digest
	if json.Unmarshal(rootfs["type"], &kind) != nil || kind != "layers" || json.Unmarshal(rootfs["diff_ids"], &diffIDs) != nil {
		return fail()
	}
	subject, err := deploy.RootFSSubject(diffIDs)
	if err != nil || subject != image.RootFSSubject {
		return fail()
	}
	var manifest []map[string]json.RawMessage
	if json.Unmarshal(archive.manifest, &manifest) != nil || len(manifest) != 1 || rejectHealthFieldAliases(manifest[0], []string{"Config", "Layers"}) != nil {
		return fail()
	}
	var ordered []string
	if json.Unmarshal(manifest[0]["Layers"], &ordered) != nil || len(ordered) != len(diffIDs) || len(ordered) == 0 || len(ordered) > imageIdentityLayerLimitV1 {
		return fail()
	}
	files := map[string][]byte{}
	destinations := imageDestinationIndexV1{}
	for index, name := range ordered {
		layer, exists := layers.layers[name]
		if !exists || layer.digest != diffIDs[index] {
			return fail()
		}
		if err := destinations.apply(ctx, layer.entries, layers.projectionBudget); err != nil {
			return fail()
		}
		for account, content := range layer.files {
			files[account] = content
		}
	}
	evidence, err := deploy.VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err != nil {
		return fail()
	}
	projection := &deploy.ImageDestinationEvidenceV1{Schema: deploy.ImageDestinationEvidenceSchemaV1, Image: image, Platform: platform, SelectionDigest: selection, RootWritable: document.Environment.ImageRootWritable(), Destinations: []deploy.ImageDestinationV1{}}
	names := make([]string, 0, len(document.Docker.Mounts))
	for name := range document.Docker.Mounts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mount := document.Docker.Mounts[name]
		target := mount.Contract.Target
		resolved, kind, content, err := destinations.destination(ctx, target)
		if err != nil || (mount.Contract.Writable && kind == "non-directory") {
			return fail()
		}
		projection.Destinations = append(projection.Destinations, deploy.ImageDestinationV1{Name: name, Target: target, Resolved: resolved, Kind: kind, ContainsContent: content})
	}
	if ctx.Err() != nil || deploy.ValidateImageDestinationEvidenceV1(projection, image, platform) != nil {
		return fail()
	}
	return preparedImageEvidenceV1{Identity: evidence, Destinations: projection}, nil
}
