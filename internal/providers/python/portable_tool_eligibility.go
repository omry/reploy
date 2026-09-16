package python

import (
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/portabletool"
	providerapi "github.com/omry/reploy/internal/providers"
)

// ValidatePortableToolPythonBindingsV1 is the pure pre-acquisition gate for
// one Python component. It consumes only canonical projection data and facts
// already observed from the selected interpreter; it performs no I/O and does
// not reconstruct pip's wheel-compatibility rules.
func ValidatePortableToolPythonBindingsV1(
	component PortableToolPythonComponentV1,
	interpreter providerapi.ExecutableEvidence,
	platform blueprint.Platform,
) error {
	projection := PortableToolPythonProjectionV1{
		Schema:     PortableToolPythonProjectionSchemaV1,
		Components: []PortableToolPythonComponentV1{component},
	}
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err != nil {
		return err
	}
	if err := platform.Validate(); err != nil {
		return fmt.Errorf("portable Python binding target platform: %w", err)
	}
	facts, err := DecodeInterpreterFactsV2(interpreter.Facts)
	if err != nil {
		return fmt.Errorf("portable Python binding interpreter evidence: %w", err)
	}
	if !stringSlicesEqualV1(facts.TestedTags, component.TestedTags) {
		return fmt.Errorf("portable Python component %q interpreter tested tags do not match its projection", component.Component)
	}

	for _, binding := range component.Bindings {
		if binding.Wheel.Platform != platform.Canonical {
			return fmt.Errorf("portable Python binding %q artifact targets %q, not %q", binding.Distribution, binding.Wheel.Platform, platform.Canonical)
		}
		claims, err := providerapi.IntersectSupportedPythonClaimsV1(binding.SupportedPython, []string{facts.Version})
		if err != nil {
			return fmt.Errorf("portable Python binding %q supported interpreter claims: %w", binding.Distribution, err)
		}
		if len(claims) == 0 {
			return fmt.Errorf("portable Python binding %q does not support inspected interpreter %s", binding.Distribution, facts.Version)
		}
		matchesRequiresPython, err := InterpreterVersionSatisfies(binding.Wheel.RequiresPython, facts.Version)
		if err != nil {
			return fmt.Errorf("portable Python binding %q Requires-Python: %w", binding.Distribution, err)
		}
		if !matchesRequiresPython {
			return fmt.Errorf("portable Python binding %q Requires-Python excludes inspected interpreter %s", binding.Distribution, facts.Version)
		}
		if err := validatePortableToolPythonBindingTagsV1(binding, facts); err != nil {
			return err
		}
	}
	return nil
}

func validatePortableToolPythonBindingTagsV1(
	binding PortableToolPythonBindingV1,
	facts InterpreterInspectionFactsV2,
) error {
	_, err := portableToolPythonBindingEligibleFilenameTagsV1(binding, facts)
	return err
}

// portableToolPythonBindingEligibleFilenameTagsV1 returns the exact sorted
// subset of the selected wheel's expanded filename tags that the selected
// interpreter accepted. The contract-advertised subset remains the first
// filter: a compatible tag that was not advertised by the binding cannot make
// an artifact eligible. The manylinux libc guard is deliberately kept here so
// the pre-acquisition gate and post-acquisition verifier share one decision.
func portableToolPythonBindingEligibleFilenameTagsV1(
	binding PortableToolPythonBindingV1,
	facts InterpreterInspectionFactsV2,
) ([]string, error) {
	lastReason := ""
	eligible := []string{}
	for _, tag := range binding.Wheel.Tags {
		if !containsSortedStringV1(binding.SupportedTags, tag) {
			continue
		}
		parts := strings.Split(tag, "-")
		if len(parts) != 3 {
			return nil, fmt.Errorf("portable Python binding %q supported tag %q is malformed", binding.Distribution, tag)
		}
		platform, err := portabletool.ProjectWheelPlatformV1(parts[2])
		if err != nil {
			return nil, fmt.Errorf("portable Python binding %q supported tag %q: %w", binding.Distribution, tag, err)
		}
		if platform.Kind == portabletool.WheelPlatformManylinuxV1 && (facts.Libc != "glibc" || facts.LibcMajor != "2") {
			lastReason = fmt.Sprintf("tag %q requires observed glibc major 2", tag)
			continue
		}
		if containsSortedStringV1(facts.CompatibleTags, tag) {
			eligible = append(eligible, tag)
			continue
		}
		lastReason = fmt.Sprintf("tag %q is absent from the inspected interpreter's compatible tags", tag)
	}
	if len(eligible) != 0 {
		return eligible, nil
	}
	return nil, fmt.Errorf("portable Python binding %q has no eligible wheel tag: %s", binding.Distribution, lastReason)
}
