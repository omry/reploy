package apt

import "github.com/omry/reploy/internal/blueprint"

const (
	WellKnownToolsProfileV1 = "well-known-apt-tools-v1"
	WellKnownToolSchemaV1   = "well-known-apt-tool-v1"
)

// WellKnownToolMappingV1 is the APT-owned provenance input for a built-in
// executable candidate. It asserts only a candidate path and consumer kind;
// the consumer must inspect the executable and determine its actual identity
// and version.
type WellKnownToolMappingV1 struct {
	Schema              string
	Profile             string
	PackageName         string
	PackageVersion      string
	OutputName          string
	CandidatePath       string
	ConsumerKind        string
	ExplicitReplacement bool
}

// ResolveWellKnownToolV1 returns the sole v1 built-in mapping. A pinned
// python3 request is still an exact python3 package request. An explicit
// python export replaces only the built-in candidate path.
func ResolveWellKnownToolV1(request blueprint.APTPackageRequest) (WellKnownToolMappingV1, bool, error) {
	if err := blueprint.ValidateAPTPackageRequest(request); err != nil {
		return WellKnownToolMappingV1{}, false, err
	}
	if request.Name != "python3" {
		return WellKnownToolMappingV1{}, false, nil
	}

	candidatePath := "/usr/bin/python3"
	explicitReplacement := false
	if export, exists := request.Exports["python"]; exists {
		candidatePath = export.Executable
		explicitReplacement = true
	}
	return WellKnownToolMappingV1{
		Schema:              WellKnownToolSchemaV1,
		Profile:             WellKnownToolsProfileV1,
		PackageName:         request.Name,
		PackageVersion:      request.Version,
		OutputName:          "python",
		CandidatePath:       candidatePath,
		ConsumerKind:        "python",
		ExplicitReplacement: explicitReplacement,
	}, true, nil
}
