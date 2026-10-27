package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

// Use the selected generated builder only. No tag, inspect, pull or image execution.
func exportDockerImageIdentityV1(ctx context.Context, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) (deploy.ImageIdentityEvidenceV1, error) {
	builder := currentGeneratedImageBuilder(ctx)
	if builder == nil || image.Validate() != nil || policy == nil || policy.Validate() != nil || platform.Validate() != nil || platform.OS != "linux" {
		return deploy.ImageIdentityEvidenceV1{}, invalidImageIdentityArchiveV1()
	}
	var evidence deploy.ImageIdentityEvidenceV1
	err := streamDockerImageArchiveV1(ctx, builder.endpoint, string(image.ConfigDigest), func(ctx context.Context, input io.Reader) error {
		var err error
		evidence, err = readDockerImageIdentityArchiveV1(ctx, input, policy, image, platform)
		return err
	})
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return deploy.ImageIdentityEvidenceV1{}, fmt.Errorf("image identity export failed (exit %d)", exit.ExitCode())
		}
		return deploy.ImageIdentityEvidenceV1{}, fmt.Errorf("image identity export failed")
	}
	return evidence, nil
}

func readDockerImageIdentityArchiveV1(ctx context.Context, input io.Reader, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) (deploy.ImageIdentityEvidenceV1, error) {
	fail := func() (deploy.ImageIdentityEvidenceV1, error) {
		return deploy.ImageIdentityEvidenceV1{}, invalidImageIdentityArchiveV1()
	}
	if image.Validate() != nil || policy == nil || policy.Validate() != nil || platform.Validate() != nil || platform.OS != "linux" {
		return fail()
	}
	layers := &imageIdentityLayersV1{}
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
	for index, name := range ordered {
		layer, exists := layers.layers[name]
		if !exists || layer.digest != diffIDs[index] {
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
	return evidence, nil
}
