package deploy

import (
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const PortableRuntimeLayerSchemaV1 = "portable-runtime-layer-v1"

// PortableRuntimeLayerV1 connects the provider graph to an intervening
// application layer containing selected payloads or runtime environment.
// The portable lock supplies the exact byte and configuration authority.
type PortableRuntimeLayerV1 struct {
	Schema            string                    `json:"schema"`
	Upstream          providers.RealizedImageV1 `json:"upstream"`
	Result            providers.RealizedImageV1 `json:"result"`
	TransactionDigest canonical.Digest          `json:"transaction_digest"`
}

func PortableRuntimeLayerTransactionDigestV1(lock providers.PortableToolLockV1, upstream, result providers.RealizedImageV1) (canonical.Digest, error) {
	if err := providers.ValidatePortableToolLockV1(lock); err != nil {
		return "", err
	}
	if err := upstream.Validate(); err != nil {
		return "", err
	}
	if err := result.Validate(); err != nil {
		return "", err
	}
	return canonical.Sum("portable-runtime-layer", PortableRuntimeLayerSchemaV1, struct {
		Lock     providers.PortableToolLockV1 `json:"lock"`
		Upstream providers.RealizedImageV1    `json:"upstream"`
		Result   providers.RealizedImageV1    `json:"result"`
	}{Lock: lock, Upstream: upstream, Result: result})
}

func validateBuildLockPortableRuntimeLayerV1(lock BuildLockV1) error {
	selected := false
	if lock.PortableTools != nil {
		for _, tool := range lock.PortableTools.Plan.PortableToolPlan.Tools {
			if strings.HasPrefix(tool.Scope, "application:") &&
				(len(tool.Responsibilities.Payloads) != 0 || (tool.Runtime != nil && len(tool.Runtime.Environment) != 0)) {
				selected = true
			}
		}
	}
	if lock.PortableRuntimeLayer == nil {
		return nil
	}
	if !selected {
		return fmt.Errorf("build lock portable runtime layer has no selected application payloads or runtime environment")
	}
	layer := lock.PortableRuntimeLayer
	if layer.Schema != PortableRuntimeLayerSchemaV1 {
		return fmt.Errorf("build lock portable runtime layer schema must be %q", PortableRuntimeLayerSchemaV1)
	}
	if err := layer.Result.Validate(); err != nil {
		return fmt.Errorf("build lock portable runtime layer result: %w", err)
	}
	if layer.Result == layer.Upstream {
		return fmt.Errorf("build lock portable runtime layer did not change the upstream image")
	}
	want, err := PortableRuntimeLayerTransactionDigestV1(*lock.PortableTools, layer.Upstream, layer.Result)
	if err != nil {
		return fmt.Errorf("build lock portable runtime layer transaction: %w", err)
	}
	if layer.TransactionDigest != want {
		return fmt.Errorf("build lock portable runtime layer transaction digest differs from selected portable lock")
	}
	return nil
}
