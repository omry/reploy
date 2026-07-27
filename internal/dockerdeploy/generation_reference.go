package dockerdeploy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
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
