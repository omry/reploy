package toolcatalog

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestResolveEmbeddedPortableToolPlanV1CompilesExactJavaClosure(t *testing.T) {
	operation := solverTestOperationV1()
	operation.Catalog = canonical.Envelope{
		Schema: "caller-fabricated-catalog-v1",
		Value:  canonical.Object{"identity": "caller-fabricated"},
	}
	operation.Platform = canonical.Envelope{
		Schema: "caller-fabricated-platform-v1",
		Value:  canonical.Object{"identity": "caller-fabricated-platform"},
	}
	plan, resolution, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{javaOwnedBuilderGroupV1()},
		javaTargetV1("debian", "12"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		[]ProviderDomainSetV1{ownedBuilderDomainsV1("source-builder:omegaconf")},
		operation,
	)
	if err != nil {
		t.Fatalf("resolve embedded Java plan: %v", err)
	}
	if len(plan.Tools) != 1 || len(resolution.Closures) != 1 {
		t.Fatalf("plan tools = %d and closures = %d, want one each", len(plan.Tools), len(resolution.Closures))
	}
	entry := plan.Tools[0]
	closure := resolution.Closures[0]
	if entry.Scope != "source-builder:omegaconf" || entry.Provenance.Tool != "java" ||
		entry.Provenance.Version != "21" || entry.Provenance.Revision != "1" ||
		entry.SelectedClosureDigest != closure.Identity ||
		len(entry.Exports) != 2 ||
		entry.Exports[0].Name != "java" ||
		entry.Exports[0].Path != "/opt/reploy/tools/java/jdk-21.0.12+8/bin/java" ||
		entry.Exports[1].Name != "javac" ||
		entry.Exports[1].Path != "/opt/reploy/tools/java/jdk-21.0.12+8/bin/javac" {
		t.Fatalf("compiled Java plan entry = %#v", entry)
	}
	if resolution.Snapshot.Digest == "" || strings.Contains(resolution.Snapshot.CanonicalJSON, "caller-fabricated") ||
		!strings.Contains(resolution.Snapshot.CanonicalJSON, "portable-tool-catalog-v1") ||
		!strings.Contains(resolution.Snapshot.CanonicalJSON, "portable-tool-observed-target-v1") ||
		!strings.Contains(resolution.Snapshot.CanonicalJSON, `"os_release_id":"debian"`) {
		t.Fatalf("snapshot does not carry internally derived catalog and target identities: %s", resolution.Snapshot.CanonicalJSON)
	}
}

func TestResolveEmbeddedPortableToolPlanV1RejectsBuildContextInApplicationScope(t *testing.T) {
	group := javaOwnedBuilderGroupV1()
	group.Scope = "application:web"
	_, _, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{group},
		javaTargetV1("debian", "12"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		[]ProviderDomainSetV1{ownedBuilderDomainsV1("application:web")},
		solverTestOperationV1(),
	)
	if err == nil || !strings.Contains(err.Error(), `requires context "runtime"`) {
		t.Fatalf("error = %v, want application/build containment failure", err)
	}
}

func TestResolveEmbeddedPortableToolPlanV1RejectsUnsupportedTargetBeforeResolution(t *testing.T) {
	_, _, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{javaOwnedBuilderGroupV1()},
		javaTargetV1("ubuntu", "24.04"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		[]ProviderDomainSetV1{ownedBuilderDomainsV1("source-builder:omegaconf")},
		solverTestOperationV1(),
	)
	if err == nil || !strings.Contains(err.Error(), "no target leaf") {
		t.Fatalf("error = %v, want unsupported target failure", err)
	}
}

func javaOwnedBuilderGroupV1() CanonicalRequirementGroupV1 {
	group := javaGroupV1("==21", "build")
	group.Scope = "source-builder:omegaconf"
	return group
}

func ownedBuilderDomainsV1(scope string) ProviderDomainSetV1 {
	return ProviderDomainSetV1{
		Scope: scope, PackageManager: scope + "/package-manager",
		Filesystem: scope + "/filesystem", Environment: scope + "/environment",
		Exports: scope + "/exports", Capabilities: scope + "/capabilities",
	}
}
