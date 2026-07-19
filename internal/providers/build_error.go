package providers

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const BuildErrorSchemaV1 = "build-error-v1"

// BuildErrorV1 is safe, structured renderer input for provider build failures.
// The underlying cause remains available to errors.Is/errors.As but is not
// included in Error(), so arbitrary backend output cannot become persisted
// state or a second copy of the live diagnostic stream.
type BuildErrorV1 struct {
	Schema     string
	Code       string
	Phase      string
	Platform   *blueprint.Platform
	BaseDigest canonical.Digest
	NodeID     NodeID
	Subject    SafeErrorSubjectV1
	Correction *CorrectionV1
	CauseKind  string
	cause      error
}

type SafeErrorSubjectV1 struct {
	Component string
	Package   string
	Version   string
	Output    string
	Path      string
}

type CorrectionV1 struct {
	Kind string
	Argv []string
}

// NewBuildErrorV1 attaches an arbitrary backend cause to safe structured
// fields. Callers must pass only identifiers already validated by their
// provider boundary.
func NewBuildErrorV1(failure BuildErrorV1, cause error) *BuildErrorV1 {
	failure.Schema = BuildErrorSchemaV1
	failure.cause = cause
	return &failure
}

func (failure *BuildErrorV1) Error() string {
	if failure == nil {
		return "provider build failed"
	}
	message := "provider build failed"
	if failure.NodeID != "" {
		message += fmt.Sprintf(" for node %q", failure.NodeID)
	}
	if failure.Phase != "" {
		message += fmt.Sprintf(" in phase %q", failure.Phase)
	}
	if failure.Platform != nil {
		message += fmt.Sprintf(" on platform %q", failure.Platform.Canonical)
	}
	if failure.BaseDigest != "" {
		message += fmt.Sprintf(" with base %q", failure.BaseDigest)
	}
	if failure.Code != "" {
		message += fmt.Sprintf(" (%s)", failure.Code)
	}
	if failure.CauseKind != "" {
		message += fmt.Sprintf(" during %q", failure.CauseKind)
	}
	if correction := buildErrorCorrectionText(failure.Correction); correction != "" {
		message += ": " + correction
	}
	return message
}

func (failure *BuildErrorV1) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func buildErrorCorrectionText(correction *CorrectionV1) string {
	if correction == nil {
		return ""
	}
	switch correction.Kind {
	case "enable-platform-emulation":
		return "enable binfmt/QEMU emulation for that platform or run the build on a compatible host"
	case "retry-after-backend-recovery":
		return "verify Docker access and retry after the backend failure is resolved"
	case "select-updatable-base":
		return "select or rebuild a base image whose APT configuration can update successfully, then retry the build"
	case "fix-apt-request-or-base":
		return "fix the package request or the selected base image repositories, then retry the build"
	case "select-compatible-base":
		return "select a compatible Debian or Ubuntu base image, then retry the build"
	case "retry-materialization":
		return "fix the named package or provider input, then retry the build"
	}
	return ""
}
