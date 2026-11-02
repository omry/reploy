package deploy

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const ImageDestinationEvidenceSchemaV1 = "image-destination-evidence-v1"

// ImageDestinationEvidenceV1 is portable preparation evidence, not host admission.
// The existing immutable build lock authenticates these producer-derived facts.
type ImageDestinationEvidenceV1 struct {
	Schema          string                    `json:"schema"`
	Image           providers.RealizedImageV1 `json:"image"`
	Platform        blueprint.Platform        `json:"platform"`
	SelectionDigest canonical.Digest          `json:"selection_digest"`
	RootWritable    bool                      `json:"root_writable"`
	Destinations    []ImageDestinationV1      `json:"destinations"`
}

type ImageDestinationV1 struct {
	Name            string `json:"name"`
	Target          string `json:"target"`
	Resolved        string `json:"resolved"`
	Kind            string `json:"kind"`
	ContainsContent bool   `json:"contains_content"`
}

func ImageDestinationSelectionDigestV1(document blueprint.Document) (canonical.Digest, error) {
	if err := document.ValidateMountDeclarations(); err != nil {
		return "", err
	}
	return canonical.Sum("image-destination-selection", ImageDestinationEvidenceSchemaV1, struct {
		RootWritable bool                             `json:"root_writable"`
		Mounts       map[string]blueprint.DockerMount `json:"mounts"`
	}{document.Environment.ImageRootWritable(), document.Docker.Mounts})
}

func validImageDestinationPathV1(value string) bool {
	return value != "/" && len(value) <= 4096 && path.IsAbs(value) && path.Clean(value) == value && !strings.ContainsAny(value, "\\\x00\r\n\t")
}

func ValidateImageDestinationEvidenceV1(evidence *ImageDestinationEvidenceV1, image providers.RealizedImageV1, platform blueprint.Platform) error {
	if evidence == nil {
		return nil
	}
	fail := func() error { return fmt.Errorf("image destination evidence: invalid structure or binding") }
	if image.Validate() != nil || platform.Validate() != nil || platform.OS != "linux" || evidence.Schema != ImageDestinationEvidenceSchemaV1 || evidence.Image != image || evidence.Platform != platform || evidence.SelectionDigest.Validate() != nil || evidence.Destinations == nil {
		return fail()
	}
	for i, entry := range evidence.Destinations {
		if entry.Name == "" || i > 0 && evidence.Destinations[i-1].Name >= entry.Name || !validImageDestinationPathV1(entry.Target) || !validImageDestinationPathV1(entry.Resolved) {
			return fail()
		}
		switch entry.Kind {
		case "absent":
			if entry.ContainsContent {
				return fail()
			}
		case "directory":
		case "non-directory":
			if !entry.ContainsContent {
				return fail()
			}
		default:
			return fail()
		}
	}
	return nil
}

// Require exact selected intent. Rebinding a blueprint cannot manufacture facts
// for a changed image, mount list, metadata contract or intrinsic root policy.
func ValidateSelectedImageDestinationsV1(document blueprint.Document, lock BuildLockV1) error {
	evidence := lock.ImageDestinations
	if document.Environment.Runtime.IdentityPolicy == nil {
		if evidence != nil {
			return fmt.Errorf("image destination evidence requires mapped identity policy")
		}
		return nil
	}
	digest, err := ImageDestinationSelectionDigestV1(document)
	if err != nil {
		return err
	}
	if evidence == nil || evidence.SelectionDigest != digest || evidence.RootWritable != document.Environment.ImageRootWritable() {
		return fmt.Errorf("image destination evidence missing or stale; rebuild required")
	}
	if err := ValidateImageDestinationEvidenceV1(evidence, lock.FinalImage, lock.Platform); err != nil {
		return err
	}
	names := make([]string, 0, len(document.Docker.Mounts))
	for name := range document.Docker.Mounts {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) != len(evidence.Destinations) {
		return fmt.Errorf("image destination inventory differs from selected mounts")
	}
	for i, name := range names {
		entry := evidence.Destinations[i]
		if entry.Name != name || entry.Target != document.Docker.Mounts[name].Contract.Target {
			return fmt.Errorf("image destination inventory differs from selected mounts")
		}
	}
	return nil
}
