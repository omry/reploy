package providers

import "github.com/omry/reploy/internal/portabletool"

// ValidatePortableToolCatalogRecordV1 is retained at the provider boundary
// for compatibility with lock construction and replay. Record-local schema
// validation is owned by the shared portable-tool contract package.
func ValidatePortableToolCatalogRecordV1(record CanonicalProviderData) error {
	return portabletool.ValidateRecordEnvelopeV1(record)
}
