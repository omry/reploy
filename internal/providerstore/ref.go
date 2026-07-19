package providerstore

import (
	"fmt"

	"github.com/omry/reploy/internal/canonical"
)

const (
	BlobKind             = "blob"
	BundleManifestKind   = "bundle-manifest"
	ValidationRecordKind = "validation-record"
)

type StoreObjectRef struct {
	Kind   string           `json:"kind"`
	Digest canonical.Digest `json:"digest"`
}

func (reference StoreObjectRef) Validate() error {
	switch reference.Kind {
	case BlobKind, BundleManifestKind, ValidationRecordKind:
	default:
		return fmt.Errorf("store object kind %q must be blob, bundle-manifest, or validation-record", reference.Kind)
	}
	if err := reference.Digest.Validate(); err != nil {
		return fmt.Errorf("store object digest: %w", err)
	}
	return nil
}
