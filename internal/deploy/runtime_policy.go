package deploy

import (
	"fmt"
	"path"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const (
	RuntimePolicySchemaV1 = "runtime-policy-v1"
	ReployImageRoot       = "/opt/reploy"
	ReployProviderRoot    = "/opt/reploy/providers"

	ProtectedPathReployRoot     = "reploy-root"
	ProtectedPathProviderRoot   = "provider-root"
	ProtectedPathProviderLeaf   = "provider-leaf"
	ProtectedPathExecutablePath = "executable-path"

	RuntimeMountSourceFile      = "file"
	RuntimeMountSourceDirectory = "directory"
	RuntimeMountSourceGenerated = "generated"
)

type RuntimePolicyV1 struct {
	Schema         string            `json:"schema"`
	ProtectedPaths []ProtectedPathV1 `json:"protected_paths"`
	Plans          []RuntimePlanV1   `json:"plans"`
}

type ProtectedPathV1 struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Owner string `json:"owner"`
}

type RuntimePlanV1 struct {
	ID          string                      `json:"id"`
	Mounts      []RuntimeMountV1            `json:"mounts"`
	Executables []providers.QualifiedOutput `json:"executables"`
}

type RuntimeMountV1 struct {
	Destination string `json:"destination"`
	SourceKind  string `json:"source_kind"`
	ReadOnly    bool   `json:"read_only"`
}

func RuntimePolicyDigestV1(policy RuntimePolicyV1) (canonical.Digest, error) {
	if err := ValidateRuntimePolicyV1(policy); err != nil {
		return "", err
	}
	return canonical.Sum("runtime-policy", RuntimePolicySchemaV1, policy)
}

func ValidateRuntimePolicyV1(policy RuntimePolicyV1) error {
	if policy.Schema != RuntimePolicySchemaV1 {
		return fmt.Errorf("runtime policy schema must be %q", RuntimePolicySchemaV1)
	}
	if policy.ProtectedPaths == nil || policy.Plans == nil {
		return fmt.Errorf("runtime policy collections must use arrays")
	}
	for index, protected := range policy.ProtectedPaths {
		if err := validateRuntimeAbsolutePath("protected path", protected.Path); err != nil {
			return err
		}
		if index > 0 && policy.ProtectedPaths[index-1].Path >= protected.Path {
			return fmt.Errorf("runtime policy protected paths must be unique and sorted")
		}
		switch protected.Kind {
		case ProtectedPathReployRoot, ProtectedPathProviderRoot, ProtectedPathProviderLeaf, ProtectedPathExecutablePath:
		default:
			return fmt.Errorf("runtime policy protected path kind %q is unsupported", protected.Kind)
		}
		if !safeRecoveryIdentity(protected.Owner) {
			return fmt.Errorf("runtime policy protected path owner must be nonempty safe text")
		}
	}
	for index, plan := range policy.Plans {
		if !safeRecoveryIdentity(plan.ID) {
			return fmt.Errorf("runtime plan ID must be nonempty safe text")
		}
		if index > 0 && policy.Plans[index-1].ID >= plan.ID {
			return fmt.Errorf("runtime plans must be unique and sorted by ID")
		}
		if plan.Mounts == nil || plan.Executables == nil {
			return fmt.Errorf("runtime plan %q collections must use arrays", plan.ID)
		}
		for mountIndex, mount := range plan.Mounts {
			if err := validateRuntimeAbsolutePath("mount destination", mount.Destination); err != nil {
				return fmt.Errorf("runtime plan %q: %w", plan.ID, err)
			}
			if err := validateRuntimeReservedDestination(mount.Destination); err != nil {
				return fmt.Errorf("runtime plan %q: %w", plan.ID, err)
			}
			if mountIndex > 0 && plan.Mounts[mountIndex-1].Destination >= mount.Destination {
				return fmt.Errorf("runtime plan %q mounts must be unique and sorted by destination", plan.ID)
			}
			switch mount.SourceKind {
			case RuntimeMountSourceFile, RuntimeMountSourceDirectory, RuntimeMountSourceGenerated:
			default:
				return fmt.Errorf("runtime plan %q mount source kind %q is unsupported", plan.ID, mount.SourceKind)
			}
		}
		for outputIndex, output := range plan.Executables {
			if err := blueprint.ValidateContributionReference("runtime executable contribution", output.Component); err != nil {
				return fmt.Errorf("runtime plan %q: %w", plan.ID, err)
			}
			if err := blueprint.ValidateProviderIdentifier("runtime executable output", output.Name); err != nil {
				return fmt.Errorf("runtime plan %q: %w", plan.ID, err)
			}
			if outputIndex > 0 && compareRuntimeOutputs(plan.Executables[outputIndex-1], output) >= 0 {
				return fmt.Errorf("runtime plan %q executables must be unique and sorted", plan.ID)
			}
		}
	}
	return nil
}

func validateRuntimeReservedDestination(destination string) error {
	if destination == "/" {
		return fmt.Errorf("mount destination must not be the container filesystem root")
	}
	if err := blueprint.ValidateRuntimeMountDestination(destination); err != nil {
		return fmt.Errorf("mount destination %q %w", destination, err)
	}
	return nil
}

func validateRuntimeAbsolutePath(field string, value string) error {
	if value == "" || !path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, `\`) {
		return fmt.Errorf("runtime policy %s %q must be a normalized absolute Linux path", field, value)
	}
	return nil
}

func runtimePathsOverlap(left string, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func compareRuntimeOutputs(left providers.QualifiedOutput, right providers.QualifiedOutput) int {
	if left.Component != right.Component {
		return strings.Compare(left.Component, right.Component)
	}
	return strings.Compare(left.Name, right.Name)
}
