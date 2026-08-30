package toolcatalog

import (
	"fmt"

	"github.com/omry/reploy/internal/canonical"
)

// ValidationProfileDigestV1 validates a complete portable-tool validation
// profile and returns its canonical record identity.
func ValidationProfileDigestV1(profile ValidationProfileRecordV1) (canonical.Digest, error) {
	record := loadedRecordV1{
		ID: profile.ID, Schema: profile.Schema, Value: &profile,
	}
	if err := validateLoadedRecordV1(record); err != nil {
		return "", fmt.Errorf("validation profile: %w", err)
	}
	digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, &profile)
	if err != nil {
		return "", fmt.Errorf("validation profile digest: %w", err)
	}
	return digest, nil
}
