package providerstore

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func storeRefDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func TestStoreObjectRefValidation(t *testing.T) {
	if err := (StoreObjectRef{Kind: "artifact", Digest: storeRefDigest("a")}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []StoreObjectRef{
		{Kind: "Artifact", Digest: storeRefDigest("a")},
		{Kind: "artifact", Digest: "bad"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid store reference was accepted: %#v", invalid)
		}
	}
}
