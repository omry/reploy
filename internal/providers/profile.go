package providers

import (
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const (
	RequirementProfileSchemaV1 = "requirement-profile-v1"
	ValidationEvidenceSchemaV1 = "validation-evidence-v1"
	ExecutableEvidenceSchemaV1 = "executable-evidence-v1"
	FileEvidenceSchemaV1       = "file-evidence-v1"
	PortableAccessSchemaV1     = "portable-access-evidence-v1"
	PortableOutputAccessV1     = "portable-output-access-v1"
)

type RequirementProfile struct {
	Schema              string                  `json:"schema"`
	Provider            blueprint.ComponentType `json:"provider"`
	Declaration         RequirementDeclaration  `json:"declaration"`
	SelectedExecutables []ExecutableEvidence    `json:"selected_executables"`
	SelectedFiles       []FileEvidence          `json:"selected_files"`
	Platform            blueprint.Platform      `json:"platform"`
	Facts               CanonicalProviderData   `json:"facts"`
}

type ValidationEvidence struct {
	Schema        string           `json:"schema"`
	SubjectRootFS canonical.Digest `json:"subject_rootfs"`
	ProfileDigest canonical.Digest `json:"profile_digest"`
}

type ExecutableEvidence struct {
	Schema         string                 `json:"schema"`
	RequirementID  string                 `json:"requirement_id"`
	Output         QualifiedOutput        `json:"output"`
	InvocationPath string                 `json:"invocation_path"`
	LinkChain      []LinkEvidence         `json:"link_chain"`
	Terminal       FileEvidence           `json:"terminal"`
	Access         PortableAccessEvidence `json:"access"`
	Facts          CanonicalProviderData  `json:"facts"`
}

type LinkEvidence struct {
	Path           string                 `json:"path"`
	Target         string                 `json:"target"`
	ResolvedPath   string                 `json:"resolved_path"`
	Kind           string                 `json:"kind"`
	Owner          *OwnerEvidence         `json:"owner,omitempty"`
	ProviderDetail *CanonicalProviderData `json:"provider_detail,omitempty"`
}

type OwnerEvidence struct {
	Provider string                `json:"provider"`
	Data     CanonicalProviderData `json:"data"`
}

type FileEvidence struct {
	Schema        string           `json:"schema"`
	RequirementID string           `json:"requirement_id"`
	Path          string           `json:"path"`
	Kind          string           `json:"kind"`
	Mode          string           `json:"mode"`
	Size          string           `json:"size"`
	SHA256        canonical.Digest `json:"sha256"`
	Owner         *OwnerEvidence   `json:"owner,omitempty"`
}

type PortableAccessEvidence struct {
	Schema  string               `json:"schema"`
	Profile string               `json:"profile"`
	Paths   []AccessPathEvidence `json:"paths"`
}

type AccessPathEvidence struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Mode     string `json:"mode"`
	Required string `json:"required"`
}

type RequirementProfileOwnerValidator func(RequirementProfile) error

func RequirementProfileDigest(profile RequirementProfile, validateOwner RequirementProfileOwnerValidator) (canonical.Digest, error) {
	if err := ValidateRequirementProfile(profile, validateOwner); err != nil {
		return "", err
	}
	return canonical.Sum("requirement-profile", RequirementProfileSchemaV1, profile)
}

func ValidateRequirementProfile(profile RequirementProfile, validateOwner RequirementProfileOwnerValidator) error {
	if profile.Schema != RequirementProfileSchemaV1 {
		return fmt.Errorf("requirement profile schema must be %q", RequirementProfileSchemaV1)
	}
	if err := validateComponentProvider(profile.Provider); err != nil {
		return fmt.Errorf("requirement profile provider: %w", err)
	}
	if err := ValidateRequirementDeclaration(profile.Declaration); err != nil {
		return fmt.Errorf("requirement profile declaration: %w", err)
	}
	if profile.SelectedExecutables == nil || profile.SelectedFiles == nil {
		return fmt.Errorf("requirement profile selected evidence must use arrays")
	}
	executableRequirements := make(map[string]ExecutableRequirement, len(profile.Declaration.Executables))
	for _, requirement := range profile.Declaration.Executables {
		executableRequirements[requirement.ID] = requirement
	}
	for index, evidence := range profile.SelectedExecutables {
		if index > 0 && profile.SelectedExecutables[index-1].RequirementID >= evidence.RequirementID {
			return fmt.Errorf("selected executable evidence must be unique and sorted by requirement ID")
		}
		requirement, exists := executableRequirements[evidence.RequirementID]
		if !exists {
			return fmt.Errorf("executable evidence %q has no declaration", evidence.RequirementID)
		}
		if err := validateExecutableEvidence(evidence, requirement); err != nil {
			return err
		}
		delete(executableRequirements, evidence.RequirementID)
	}
	if len(executableRequirements) != 0 {
		return fmt.Errorf("requirement profile is missing selected executable evidence")
	}
	fileRequirements := make(map[string]FileRequirement, len(profile.Declaration.Files))
	for _, requirement := range profile.Declaration.Files {
		fileRequirements[requirement.ID] = requirement
	}
	for index, evidence := range profile.SelectedFiles {
		if index > 0 && profile.SelectedFiles[index-1].RequirementID >= evidence.RequirementID {
			return fmt.Errorf("selected file evidence must be unique and sorted by requirement ID")
		}
		requirement, exists := fileRequirements[evidence.RequirementID]
		if !exists {
			return fmt.Errorf("file evidence %q has no declaration", evidence.RequirementID)
		}
		if err := validateFileEvidence(evidence, false); err != nil {
			return err
		}
		if evidence.Path != requirement.Path || evidence.Kind != requirement.Kind {
			return fmt.Errorf("file evidence %q does not match its declared path and kind", evidence.RequirementID)
		}
		if requirement.ExpectedSHA256 != "" && evidence.SHA256 != requirement.ExpectedSHA256 {
			return fmt.Errorf("file evidence %q does not match its declared digest", evidence.RequirementID)
		}
		delete(fileRequirements, evidence.RequirementID)
	}
	if len(fileRequirements) != 0 {
		return fmt.Errorf("requirement profile is missing selected file evidence")
	}
	if err := profile.Platform.Validate(); err != nil {
		return fmt.Errorf("requirement profile platform: %w", err)
	}
	if err := validateCanonicalProviderData("requirement profile facts", profile.Facts); err != nil {
		return err
	}
	if validateOwner == nil {
		return fmt.Errorf("requirement profile provider-owned validator is required")
	}
	if err := validateOwner(profile); err != nil {
		return fmt.Errorf("requirement profile provider facts: %w", err)
	}
	return nil
}

func NewValidationEvidence(subject canonical.Digest, profileDigest canonical.Digest) (ValidationEvidence, error) {
	evidence := ValidationEvidence{Schema: ValidationEvidenceSchemaV1, SubjectRootFS: subject, ProfileDigest: profileDigest}
	if err := evidence.Validate(); err != nil {
		return ValidationEvidence{}, err
	}
	return evidence, nil
}

func (evidence ValidationEvidence) Validate() error {
	if evidence.Schema != ValidationEvidenceSchemaV1 {
		return fmt.Errorf("validation evidence schema must be %q", ValidationEvidenceSchemaV1)
	}
	if err := evidence.SubjectRootFS.Validate(); err != nil {
		return fmt.Errorf("validation evidence rootfs subject: %w", err)
	}
	if err := evidence.ProfileDigest.Validate(); err != nil {
		return fmt.Errorf("validation evidence profile digest: %w", err)
	}
	return nil
}

func validateExecutableEvidence(evidence ExecutableEvidence, requirement ExecutableRequirement) error {
	if evidence.Output.Name != requirement.Command {
		return fmt.Errorf("executable evidence %q selects output %q, want %q", evidence.RequirementID, evidence.Output.Name, requirement.Command)
	}
	if requirement.Supplier != "" && evidence.Output.Component != requirement.Supplier {
		return fmt.Errorf("executable evidence %q selects component %q, want %q", evidence.RequirementID, evidence.Output.Component, requirement.Supplier)
	}
	return validateExecutableEvidenceStructure(evidence, false)
}

func validateExecutableEvidenceStructure(evidence ExecutableEvidence, allowEmptyRequirement bool) error {
	if evidence.Schema != ExecutableEvidenceSchemaV1 {
		return fmt.Errorf("executable evidence %q schema must be %q", evidence.RequirementID, ExecutableEvidenceSchemaV1)
	}
	if err := blueprint.ValidateContributionReference("executable evidence contribution", evidence.Output.Component); err != nil {
		return err
	}
	if err := blueprint.ValidateProviderIdentifier("executable evidence output", evidence.Output.Name); err != nil {
		return err
	}
	if err := validateAbsoluteLinuxPath("executable invocation path", evidence.InvocationPath); err != nil {
		return err
	}
	if evidence.LinkChain == nil {
		return fmt.Errorf("executable evidence %q link chain must use an array", evidence.RequirementID)
	}
	seenLinks := make(map[string]bool, len(evidence.LinkChain))
	for index, link := range evidence.LinkChain {
		if err := validateLinkEvidence(link); err != nil {
			return fmt.Errorf("executable evidence %q: %w", evidence.RequirementID, err)
		}
		if seenLinks[link.Path] {
			return fmt.Errorf("executable evidence %q contains duplicate link path %q", evidence.RequirementID, link.Path)
		}
		seenLinks[link.Path] = true
		expectedResolvedPath := evidence.Terminal.Path
		if index+1 < len(evidence.LinkChain) {
			expectedResolvedPath = evidence.LinkChain[index+1].Path
		}
		if link.ResolvedPath != expectedResolvedPath {
			return fmt.Errorf("executable evidence %q link %q resolves to %q, want %q", evidence.RequirementID, link.Path, link.ResolvedPath, expectedResolvedPath)
		}
	}
	expectedInvocation := evidence.Terminal.Path
	if len(evidence.LinkChain) != 0 {
		expectedInvocation = evidence.LinkChain[0].Path
	}
	if evidence.InvocationPath != expectedInvocation {
		return fmt.Errorf("executable evidence %q invocation path does not begin its recorded chain", evidence.RequirementID)
	}
	if evidence.Terminal.RequirementID != evidence.RequirementID {
		return fmt.Errorf("executable evidence %q terminal uses requirement ID %q", evidence.RequirementID, evidence.Terminal.RequirementID)
	}
	if err := validateFileEvidence(evidence.Terminal, allowEmptyRequirement); err != nil {
		return err
	}
	if evidence.Terminal.Kind != "regular" {
		return fmt.Errorf("executable evidence %q terminal kind must be %q", evidence.RequirementID, "regular")
	}
	if err := validatePortableAccessEvidence(evidence.Access); err != nil {
		return err
	}
	terminalAccess := false
	for _, item := range evidence.Access.Paths {
		if item.Path == evidence.Terminal.Path && item.Kind == "regular" && item.Required == "other-read-execute" {
			terminalAccess = true
			break
		}
	}
	if !terminalAccess {
		return fmt.Errorf("executable evidence %q access record omits terminal %q", evidence.RequirementID, evidence.Terminal.Path)
	}
	return validateCanonicalProviderData("executable evidence facts", evidence.Facts)
}

func ValidateExecutableEvidence(evidence ExecutableEvidence, requirement ExecutableRequirement) error {
	return validateExecutableEvidence(evidence, requirement)
}

func ValidateFinalExecutableEvidence(evidence ExecutableEvidence) error {
	if evidence.RequirementID != "" {
		return fmt.Errorf("final executable evidence must not use a requirement ID")
	}
	return validateExecutableEvidenceStructure(evidence, true)
}

func validateLinkEvidence(evidence LinkEvidence) error {
	if err := validateAbsoluteLinuxPath("link evidence path", evidence.Path); err != nil {
		return err
	}
	if evidence.Target == "" {
		return fmt.Errorf("link evidence target is required")
	}
	if err := validateAbsoluteLinuxPath("link evidence resolved path", evidence.ResolvedPath); err != nil {
		return err
	}
	if evidence.Kind != "ordinary" && evidence.Kind != "alternative" {
		return fmt.Errorf("link evidence kind must be %q or %q", "ordinary", "alternative")
	}
	if evidence.Owner != nil {
		if err := validateOwnerEvidence(*evidence.Owner); err != nil {
			return err
		}
	}
	if evidence.ProviderDetail != nil {
		if err := validateCanonicalProviderData("link provider detail", *evidence.ProviderDetail); err != nil {
			return err
		}
	}
	return nil
}

func validateFileEvidence(evidence FileEvidence, allowEmptyRequirement bool) error {
	if evidence.Schema != FileEvidenceSchemaV1 {
		return fmt.Errorf("file evidence %q schema must be %q", evidence.RequirementID, FileEvidenceSchemaV1)
	}
	if !allowEmptyRequirement || evidence.RequirementID != "" {
		if err := blueprint.ValidateProviderIdentifier("file evidence requirement ID", evidence.RequirementID); err != nil {
			return err
		}
	}
	if err := validateAbsoluteLinuxPath("file evidence path", evidence.Path); err != nil {
		return err
	}
	if err := blueprint.ValidateProviderIdentifier("file evidence kind", evidence.Kind); err != nil {
		return err
	}
	if !isCanonicalMode(evidence.Mode) {
		return fmt.Errorf("file evidence mode %q must use four lowercase octal digits", evidence.Mode)
	}
	if !isCanonicalDecimal(evidence.Size) {
		return fmt.Errorf("file evidence size %q must be a nonnegative canonical decimal integer", evidence.Size)
	}
	if err := evidence.SHA256.Validate(); err != nil {
		return fmt.Errorf("file evidence digest: %w", err)
	}
	if evidence.Owner != nil {
		return validateOwnerEvidence(*evidence.Owner)
	}
	return nil
}

func validateOwnerEvidence(evidence OwnerEvidence) error {
	if err := blueprint.ValidateProviderIdentifier("owner provider", evidence.Provider); err != nil {
		return err
	}
	return validateCanonicalProviderData("owner data", evidence.Data)
}

func validatePortableAccessEvidence(evidence PortableAccessEvidence) error {
	if evidence.Schema != PortableAccessSchemaV1 {
		return fmt.Errorf("portable access schema must be %q", PortableAccessSchemaV1)
	}
	if evidence.Profile != PortableOutputAccessV1 {
		return fmt.Errorf("portable access profile must be %q", PortableOutputAccessV1)
	}
	if evidence.Paths == nil {
		return fmt.Errorf("portable access paths must use an array")
	}
	for index, item := range evidence.Paths {
		if index > 0 && evidence.Paths[index-1].Path >= item.Path {
			return fmt.Errorf("portable access paths must be unique and sorted")
		}
		if err := validateAbsoluteLinuxPath("portable access path", item.Path); err != nil {
			return err
		}
		if err := blueprint.ValidateProviderIdentifier("portable access kind", item.Kind); err != nil {
			return err
		}
		if !isCanonicalMode(item.Mode) {
			return fmt.Errorf("portable access mode %q must use four lowercase octal digits", item.Mode)
		}
		switch item.Required {
		case "other-search":
			if item.Kind != "directory" || item.Mode[3] < '1' || item.Mode[3] == '2' || item.Mode[3] == '4' || item.Mode[3] == '6' {
				return fmt.Errorf("portable access directory %q does not prove other-search", item.Path)
			}
		case "other-read-execute":
			if item.Kind != "regular" || item.Mode[3] != '5' && item.Mode[3] != '7' {
				return fmt.Errorf("portable access terminal %q does not prove other-read-execute", item.Path)
			}
		default:
			return fmt.Errorf("portable access required permissions %q are unsupported", item.Required)
		}
	}
	return nil
}

func isCanonicalMode(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '7' {
			return false
		}
	}
	return true
}

func isCanonicalDecimal(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	return strings.IndexFunc(value[1:], func(char rune) bool { return char < '0' || char > '9' }) == -1
}
