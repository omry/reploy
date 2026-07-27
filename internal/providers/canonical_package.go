package providers

import (
	"fmt"

	"github.com/omry/reploy/internal/canonical"
)

// CanonicalPackageRequest carries a provider-owned normalized package value
// across the deployment/provider boundary without retaining CLI syntax.
type CanonicalPackageRequest struct {
	Schema string           `json:"schema"`
	Value  canonical.Object `json:"value"`
}

// CanonicalPackageRequestBytes validates the common envelope and returns its
// canonical bytes. Provider-specific code remains responsible for validating
// the exact Value shape before constructing or consuming the request.
func CanonicalPackageRequestBytes(request CanonicalPackageRequest) ([]byte, error) {
	if request.Schema == "" {
		return nil, fmt.Errorf("canonical package request schema is required")
	}
	if request.Value == nil {
		return nil, fmt.Errorf("canonical package request value must be an object")
	}
	encoded, err := canonical.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("canonical package request %s: %w", request.Schema, err)
	}
	return encoded, nil
}
