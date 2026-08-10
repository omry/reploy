package dockerdeploy

import (
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func testApplicationSandboxPlanV1(uid uint32, gid uint32) ApplicationSandboxPlanV1 {
	return newApplicationSandboxPlanV1(RuntimeUserPlan{
		UID: uid, GID: gid, DockerUser: runtimeIDStringV1(uid) + ":" + runtimeIDStringV1(gid),
	})
}

func testResolvedBlueprintV1(t *testing.T, document blueprint.Document) blueprint.ResolvedDocumentV1 {
	t.Helper()
	payload, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func testResolvedBlueprintDigestV1(t *testing.T, document blueprint.Document) canonical.Digest {
	t.Helper()
	digest, err := blueprint.DocumentDigestV1(document)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func testSelectedPlatformDocumentV1(t *testing.T) (blueprint.Document, blueprint.Platform) {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{platform}}},
		Environment: blueprint.Environment{
			ID: "demo", ControlScript: "demo",
			Base: blueprint.BaseComponent{
				Image: "local-base", Exports: map[string]blueprint.BaseExecutableExport{},
			},
		},
	}
	return document, platform
}
