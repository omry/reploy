package dockerdeploy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/providers"
)

type GeneratedImageSlot string

const (
	GeneratedImageStaging  GeneratedImageSlot = "staging"
	GeneratedImageDeployed GeneratedImageSlot = "deployed"
	GeneratedImagePrevious GeneratedImageSlot = "previous"
)

const (
	generatedImageOwnerLabel       = "io.reploy.owner"
	generatedImageDirectoryLabel   = "io.reploy.directory"
	generatedImageEnvironmentLabel = "io.reploy.environment"
	generatedImageFingerprintLabel = "io.reploy.fingerprint"
	generatedImageBaseDigestLabel  = "io.reploy.base-digest"
)

type GeneratedImageIdentity struct {
	DirectoryID string
	Repository  string
	Reference   string
	Fingerprint string
	BaseDigest  string
	Labels      map[string]string
}

func generatedImageIdentity(environmentID string, deploymentDir string, slot GeneratedImageSlot, bundles []providers.Bundle) (GeneratedImageIdentity, error) {
	switch slot {
	case GeneratedImageStaging, GeneratedImageDeployed, GeneratedImagePrevious:
	default:
		return GeneratedImageIdentity{}, fmt.Errorf("invalid generated image slot %q", slot)
	}
	directoryID, err := pathIdentityHash(deploymentDir)
	if err != nil {
		return GeneratedImageIdentity{}, err
	}
	fingerprint, err := generatedImageFingerprint(bundles)
	if err != nil {
		return GeneratedImageIdentity{}, err
	}
	baseDigest := ""
	for _, bundle := range bundles {
		if baseDigest == "" {
			baseDigest = bundle.BaseIdentity
			continue
		}
		if bundle.BaseIdentity != baseDigest {
			return GeneratedImageIdentity{}, fmt.Errorf("provider bundles disagree on base image identity")
		}
	}
	if baseDigest == "" {
		return GeneratedImageIdentity{}, fmt.Errorf("generated image requires at least one provider bundle")
	}
	repository := "reploy/" + dockerNameSlug(environmentID, "environment") + "-" + directoryID
	reference := repository + ":" + string(slot)
	labels := map[string]string{
		generatedImageOwnerLabel:       "reploy",
		generatedImageDirectoryLabel:   directoryID,
		generatedImageEnvironmentLabel: environmentID,
		generatedImageFingerprintLabel: fingerprint,
		generatedImageBaseDigestLabel:  baseDigest,
	}
	return GeneratedImageIdentity{
		DirectoryID: directoryID, Repository: repository, Reference: reference,
		Fingerprint: fingerprint, BaseDigest: baseDigest, Labels: labels,
	}, nil
}

func generatedImageFingerprint(bundles []providers.Bundle) (string, error) {
	type artifact struct {
		Identifier string `json:"identifier"`
		Version    string `json:"version,omitempty"`
		Kind       string `json:"kind"`
		Path       string `json:"path"`
		SHA256     string `json:"sha256"`
	}
	type bundle struct {
		Provider      string     `json:"provider"`
		RecipeVersion string     `json:"recipe_version"`
		Platform      string     `json:"platform"`
		BaseIdentity  string     `json:"base_identity"`
		Artifacts     []artifact `json:"artifacts"`
	}
	input := make([]bundle, len(bundles))
	seenProviders := map[string]bool{}
	for index, item := range bundles {
		if err := providers.ValidateBundle(item); err != nil {
			return "", fmt.Errorf("fingerprint provider bundle: %w", err)
		}
		if seenProviders[string(item.Provider)] {
			return "", fmt.Errorf("provider %q produced more than one bundle", item.Provider)
		}
		seenProviders[string(item.Provider)] = true
		artifacts := make([]artifact, len(item.Artifacts))
		for artifactIndex, item := range item.Artifacts {
			artifacts[artifactIndex] = artifact{
				Identifier: item.Identifier, Version: item.Version, Kind: item.Kind,
				Path: item.Path, SHA256: item.SHA256,
			}
		}
		sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
		input[index] = bundle{
			Provider: string(item.Provider), RecipeVersion: item.RecipeVersion,
			Platform: item.Platform, BaseIdentity: item.BaseIdentity, Artifacts: artifacts,
		}
	}
	sort.Slice(input, func(i, j int) bool { return input[i].Provider < input[j].Provider })
	content, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}
