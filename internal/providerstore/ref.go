package providerstore

import (
	"fmt"

	"github.com/omry/reploy/internal/canonical"
)

const BundleManifestKind = "bundle-manifest"

type StoreObjectRef struct {
	Kind   string           `json:"kind"`
	Digest canonical.Digest `json:"digest"`
}

func (reference StoreObjectRef) Validate() error {
	if !isIdentifier(reference.Kind) {
		return fmt.Errorf("store object kind %q must use the provider identifier grammar", reference.Kind)
	}
	if err := reference.Digest.Validate(); err != nil {
		return fmt.Errorf("store object digest: %w", err)
	}
	return nil
}
