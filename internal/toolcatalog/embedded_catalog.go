package toolcatalog

import (
	"embed"
	"fmt"
	"sync"
)

//go:generate go run ./cmd/cataloggen

// definitionFilesV1 contains only generated, canonical catalog records. The
// first-party YAML sources remain reviewable authoring input and are never
// interpreted at runtime.
//
//go:embed definitions
var definitionFilesV1 embed.FS

var (
	embeddedCatalogOnceV1 sync.Once
	embeddedCatalogV1     *CatalogV1
)

func mustLoadEmbeddedCatalogV1() *CatalogV1 {
	embeddedCatalogOnceV1.Do(func() {
		catalog, err := loadCatalogV1(definitionFilesV1, "definitions")
		if err != nil {
			panic(fmt.Sprintf("load embedded portable tool catalog: %v", err))
		}
		embeddedCatalogV1 = catalog
	})
	return embeddedCatalogV1
}
