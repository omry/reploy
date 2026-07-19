// Package providerstore owns content-addressed provider artifact records and,
// later, their deployment-local publication and reachability.
package providerstore

import (
	"fmt"
	"path"
	"strings"

	"github.com/omry/reploy/internal/canonical"
)

type ArtifactDescriptor struct {
	LogicalPath string           `json:"logical_path"`
	Kind        string           `json:"kind"`
	Size        string           `json:"size"`
	SHA256      canonical.Digest `json:"sha256"`
}

func (descriptor ArtifactDescriptor) StoreObjectRef() (StoreObjectRef, error) {
	if err := descriptor.Validate(); err != nil {
		return StoreObjectRef{}, err
	}
	return StoreObjectRef{Kind: BlobKind, Digest: descriptor.SHA256}, nil
}

func (descriptor ArtifactDescriptor) Validate() error {
	if descriptor.LogicalPath == "" || path.IsAbs(descriptor.LogicalPath) || path.Clean(descriptor.LogicalPath) != descriptor.LogicalPath || strings.Contains(descriptor.LogicalPath, `\`) {
		return fmt.Errorf("artifact logical path %q must be a normalized relative slash path", descriptor.LogicalPath)
	}
	for _, part := range strings.Split(descriptor.LogicalPath, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("artifact logical path %q contains an invalid component", descriptor.LogicalPath)
		}
	}
	if !isIdentifier(descriptor.Kind) {
		return fmt.Errorf("artifact kind %q must use the provider identifier grammar", descriptor.Kind)
	}
	if !isCanonicalSize(descriptor.Size) {
		return fmt.Errorf("artifact size %q must be a nonnegative canonical decimal integer", descriptor.Size)
	}
	if err := descriptor.SHA256.Validate(); err != nil {
		return fmt.Errorf("artifact digest: %w", err)
	}
	return nil
}

func isCanonicalSize(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func isIdentifier(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
