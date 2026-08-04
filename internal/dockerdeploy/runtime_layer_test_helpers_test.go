package dockerdeploy

import (
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func testApplicationLocalAccountV1() deploy.ApplicationLocalAccountV1 {
	return deploy.ApplicationLocalAccountV1{
		Schema: deploy.ApplicationLocalAccountSchemaV1, Name: "reploy", UID: "1000", GID: "1000", Home: environmentTemporaryHome,
	}
}

func testApplicationRuntimeLayerV1(
	t *testing.T,
	platform blueprint.Platform,
	upstream providers.RealizedImageV1,
	result providers.RealizedImageV1,
) deploy.ApplicationRuntimeLayerV1 {
	t.Helper()
	verifier := deploy.ApplicationStartupVerifierContractV1()
	verifier.Artifact = rendererDigest("f")
	verifier.Size = "123"
	account := testApplicationLocalAccountV1()
	transaction, err := deploy.ApplicationRuntimeLayerTransactionDigestV1(verifier, account, upstream, platform)
	if err != nil {
		t.Fatal(err)
	}
	return deploy.ApplicationRuntimeLayerV1{
		Schema: deploy.ApplicationRuntimeLayerSchemaV1, Verifier: verifier, Account: account,
		TransactionDigest: transaction, Upstream: upstream, Result: result,
	}
}
