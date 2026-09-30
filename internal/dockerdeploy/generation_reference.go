package dockerdeploy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/omry/reploy/internal/canonical"
)

const environmentReferenceRandomBytes = 16

type EnvironmentImageReferences struct {
	Temporary  string
	Generation string
}

func NewEnvironmentImageReferences(environment string, deploymentDir string) (EnvironmentImageReferences, error) {
	return newEnvironmentImageReferences(environment, deploymentDir, rand.Reader)
}

func newEnvironmentImageReferences(environment string, deploymentDir string, random io.Reader) (EnvironmentImageReferences, error) {
	if strings.TrimSpace(environment) == "" {
		return EnvironmentImageReferences{}, fmt.Errorf("environment image reference requires an environment name")
	}
	if random == nil {
		return EnvironmentImageReferences{}, fmt.Errorf("environment image reference requires randomness")
	}
	directoryHash, err := pathIdentityHash(deploymentDir)
	if err != nil {
		return EnvironmentImageReferences{}, fmt.Errorf("environment image reference directory: %w", err)
	}
	prefix := "reploy/env/" + dockerNameSlug(environment, "environment") + "-" + directoryHash + ":"
	temporarySuffix, err := randomReferenceSuffix(random)
	if err != nil {
		return EnvironmentImageReferences{}, err
	}
	generationSuffix, err := randomReferenceSuffix(random)
	if err != nil {
		return EnvironmentImageReferences{}, err
	}
	return EnvironmentImageReferences{
		Temporary: prefix + "tmp-" + temporarySuffix, Generation: prefix + "g-" + generationSuffix,
	}, nil
}

func ValidateEnvironmentImageReferences(references EnvironmentImageReferences, environment string, deploymentDir string) error {
	if strings.TrimSpace(environment) == "" {
		return fmt.Errorf("environment image reference requires an environment name")
	}
	directoryHash, err := pathIdentityHash(deploymentDir)
	if err != nil {
		return fmt.Errorf("environment image reference directory: %w", err)
	}
	prefix := "reploy/env/" + dockerNameSlug(environment, "environment") + "-" + directoryHash + ":"
	if err := validateEnvironmentReference(references.Temporary, prefix+"tmp-"); err != nil {
		return fmt.Errorf("temporary environment image reference: %w", err)
	}
	if err := validateEnvironmentReference(references.Generation, prefix+"g-"); err != nil {
		return fmt.Errorf("generation environment image reference: %w", err)
	}
	if references.Temporary == references.Generation {
		return fmt.Errorf("temporary and generation image references must differ")
	}
	return nil
}

// NewEnvironmentPortableRuntimeLayerReference returns the deployment-owned
// provisional tag for one exact portable runtime layer. It is deliberately
// derived from the immutable config digest, so pending publication can retain
// the intermediate before it chooses its random generation reference.
func NewEnvironmentPortableRuntimeLayerReference(
	imageDigest canonical.Digest,
	environment string,
	deploymentDir string,
) (string, error) {
	prefix, err := environmentReferencePrefix(environment, deploymentDir)
	if err != nil {
		return "", err
	}
	suffix, err := portableRuntimeLayerDigestSuffix(imageDigest)
	if err != nil {
		return "", err
	}
	return prefix + "d-" + suffix, nil
}

// NewEnvironmentPortableRuntimeLayerReferenceForGeneration returns the
// deployment-owned generation-lifetime tag for one exact portable runtime
// layer. The generation's validated random suffix binds this tag to one
// generation even when another generation has the same portable-layer digest.
func NewEnvironmentPortableRuntimeLayerReferenceForGeneration(
	imageDigest canonical.Digest,
	generation string,
	environment string,
	deploymentDir string,
) (string, error) {
	prefix, err := environmentReferencePrefix(environment, deploymentDir)
	if err != nil {
		return "", err
	}
	generationPrefix, err := environmentGenerationReferencePrefix(generation)
	if err != nil {
		return "", err
	}
	if generationPrefix != prefix {
		return "", fmt.Errorf("portable runtime generation reference is not owned by this deployment")
	}
	generationSuffix := strings.TrimPrefix(generation, generationPrefix+"g-")
	suffix, err := portableRuntimeLayerDigestSuffix(imageDigest)
	if err != nil {
		return "", err
	}
	return prefix + "p-" + generationSuffix + "-" + suffix, nil
}

// portableRuntimeLayerReferenceFromGeneration reconstructs the retained tag
// from the recorded generation identity. The caller has already loaded the
// deployment state; this still validates the owned generation shape before
// deriving the reference.
func portableRuntimeLayerReferenceFromGeneration(
	imageDigest canonical.Digest,
	generation string,
) (string, error) {
	prefix, err := environmentGenerationReferencePrefix(generation)
	if err != nil {
		return "", err
	}
	generationSuffix := strings.TrimPrefix(generation, prefix+"g-")
	suffix, err := portableRuntimeLayerDigestSuffix(imageDigest)
	if err != nil {
		return "", err
	}
	return prefix + "p-" + generationSuffix + "-" + suffix, nil
}

func ValidateEnvironmentPortableRuntimeLayerReference(reference string, environment string, deploymentDir string) error {
	prefix, err := environmentReferencePrefix(environment, deploymentDir)
	if err != nil {
		return err
	}
	return validatePortableRuntimeLayerGenerationReference(reference, prefix+"p-")
}

// ValidateEnvironmentPortableRuntimeLayerReferenceShape validates the
// deployment-owned syntax used by the public verifier after it reconstructs
// the reference prefix from the recorded generation.
func ValidateEnvironmentPortableRuntimeLayerReferenceShape(reference string) error {
	prefix, _, found := strings.Cut(reference, ":p-")
	if !found || !strings.HasPrefix(prefix, "reploy/env/") {
		return fmt.Errorf("portable runtime layer reference is not deployment-owned")
	}
	return validatePortableRuntimeLayerGenerationReference(reference, prefix+":p-")
}

func ValidateEnvironmentPortableRuntimeLayerDigestReference(reference string, environment string, deploymentDir string) error {
	prefix, err := environmentReferencePrefix(environment, deploymentDir)
	if err != nil {
		return err
	}
	return validatePortableRuntimeLayerReference(reference, prefix+"d-")
}

func ValidateEnvironmentGenerationReference(reference string, environment string, deploymentDir string) error {
	if strings.TrimSpace(environment) == "" {
		return fmt.Errorf("environment image reference requires an environment name")
	}
	directoryHash, err := pathIdentityHash(deploymentDir)
	if err != nil {
		return fmt.Errorf("environment image reference directory: %w", err)
	}
	prefix := "reploy/env/" + dockerNameSlug(environment, "environment") + "-" + directoryHash + ":g-"
	if err := validateEnvironmentReference(reference, prefix); err != nil {
		return fmt.Errorf("generation environment image reference: %w", err)
	}
	return nil
}

func environmentReferencePrefix(environment string, deploymentDir string) (string, error) {
	if strings.TrimSpace(environment) == "" {
		return "", fmt.Errorf("environment image reference requires an environment name")
	}
	directoryHash, err := pathIdentityHash(deploymentDir)
	if err != nil {
		return "", fmt.Errorf("environment image reference directory: %w", err)
	}
	return "reploy/env/" + dockerNameSlug(environment, "environment") + "-" + directoryHash + ":", nil
}

func environmentGenerationReferencePrefix(reference string) (string, error) {
	marker := ":g-"
	prefix, _, found := strings.Cut(reference, marker)
	if !found || prefix == "" {
		return "", fmt.Errorf("environment generation reference has an invalid shape")
	}
	if err := validateEnvironmentReference(reference, prefix+marker); err != nil {
		return "", fmt.Errorf("environment generation reference: %w", err)
	}
	return prefix + ":", nil
}

func portableRuntimeLayerDigestSuffix(imageDigest canonical.Digest) (string, error) {
	if err := imageDigest.Validate(); err != nil {
		return "", fmt.Errorf("portable runtime layer image digest: %w", err)
	}
	const algorithm = "sha256:"
	suffix := strings.TrimPrefix(string(imageDigest), algorithm)
	if len(suffix) != 64 {
		return "", fmt.Errorf("portable runtime layer image digest has an invalid suffix")
	}
	return suffix, nil
}

func validatePortableRuntimeLayerReference(reference string, prefix string) error {
	if !strings.HasPrefix(reference, prefix) {
		return fmt.Errorf("portable runtime layer reference is not owned by this deployment")
	}
	suffix := strings.TrimPrefix(reference, prefix)
	if len(suffix) != 64 || suffix != strings.ToLower(suffix) {
		return fmt.Errorf("portable runtime layer reference has an invalid digest suffix")
	}
	if err := (canonical.Digest("sha256:" + suffix)).Validate(); err != nil {
		return fmt.Errorf("portable runtime layer reference has an invalid digest suffix")
	}
	return nil
}

func validatePortableRuntimeLayerGenerationReference(reference string, prefix string) error {
	if !strings.HasPrefix(reference, prefix) {
		return fmt.Errorf("portable runtime layer reference is not owned by this deployment")
	}
	suffix := strings.TrimPrefix(reference, prefix)
	separator := environmentReferenceRandomBytes * 2
	if len(suffix) != separator+1+64 || suffix[separator] != '-' {
		return fmt.Errorf("portable runtime layer reference has an invalid generation suffix")
	}
	generationSuffix := suffix[:separator]
	if generationSuffix != strings.ToLower(generationSuffix) {
		return fmt.Errorf("portable runtime layer reference has an invalid generation suffix")
	}
	if _, err := hex.DecodeString(generationSuffix); err != nil {
		return fmt.Errorf("portable runtime layer reference has an invalid generation suffix")
	}
	digestSuffix := suffix[separator+1:]
	if err := (canonical.Digest("sha256:" + digestSuffix)).Validate(); err != nil {
		return fmt.Errorf("portable runtime layer reference has an invalid digest suffix")
	}
	return nil
}

func randomReferenceSuffix(reader io.Reader) (string, error) {
	content := make([]byte, environmentReferenceRandomBytes)
	if _, err := io.ReadFull(reader, content); err != nil {
		return "", fmt.Errorf("generate environment image reference: %w", err)
	}
	return hex.EncodeToString(content), nil
}

func validateEnvironmentReference(reference string, prefix string) error {
	if !strings.HasPrefix(reference, prefix) {
		return fmt.Errorf("reference is not owned by this deployment")
	}
	suffix := strings.TrimPrefix(reference, prefix)
	if len(suffix) != environmentReferenceRandomBytes*2 || suffix != strings.ToLower(suffix) {
		return fmt.Errorf("reference has an invalid random suffix")
	}
	decoded, err := hex.DecodeString(suffix)
	if err != nil || len(decoded) != environmentReferenceRandomBytes {
		return fmt.Errorf("reference has an invalid random suffix")
	}
	return nil
}
