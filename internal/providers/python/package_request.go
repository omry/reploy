package python

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const PackageRequestSchemaV1 = "python-package-request-v1"

func CanonicalPackageRequestV1(requirement string) (providers.CanonicalPackageRequest, error) {
	requirement = strings.TrimSpace(requirement)
	if requirement == "" || !utf8.ValidString(requirement) {
		return providers.CanonicalPackageRequest{}, fmt.Errorf("Python package requirement must be nonempty valid UTF-8")
	}
	if strings.HasPrefix(requirement, "-") {
		return providers.CanonicalPackageRequest{}, fmt.Errorf("Python package requirement must not be a package-manager option")
	}
	for _, char := range requirement {
		if unicode.IsControl(char) {
			return providers.CanonicalPackageRequest{}, fmt.Errorf("Python package requirement must not contain control characters")
		}
	}
	return providers.CanonicalPackageRequest{
		Schema: PackageRequestSchemaV1,
		Value:  canonical.Object{"requirement": requirement},
	}, nil
}

func ValidateCanonicalPackageRequestV1(request providers.CanonicalPackageRequest) error {
	if request.Schema != PackageRequestSchemaV1 {
		return fmt.Errorf("Python package request schema must be %q", PackageRequestSchemaV1)
	}
	if len(request.Value) != 1 {
		return fmt.Errorf("Python package request must contain exactly requirement")
	}
	requirement, ok := request.Value["requirement"].(string)
	if !ok {
		return fmt.Errorf("Python package request requirement must be a string")
	}
	normalized, err := CanonicalPackageRequestV1(requirement)
	if err != nil {
		return err
	}
	actual, err := providers.CanonicalPackageRequestBytes(request)
	if err != nil {
		return err
	}
	expected, err := providers.CanonicalPackageRequestBytes(normalized)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return fmt.Errorf("Python package request is not canonically normalized")
	}
	return nil
}
