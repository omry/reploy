package toolcatalog

import (
	"github.com/omry/reploy/internal/canonical"
	"testing"
)

func TestEmbeddedExecutableArchivesHaveRuntimeClosureAndRejectWrongPlatform(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	for name, version := range map[string]string{"anydesk": "8.1.0", "bws": "2.1.0"} {
		group := CanonicalRequirementGroupV1{Scope: "application:desktop", Tool: name, VersionConstraints: []string{"==" + version}, Context: "runtime", Binding: CanonicalBindingDemandV1{Infer: true}, Selections: map[string][]string{}}
		target := javaTargetV1("debian", "12")
		candidates, err := catalog.SelectReleaseCandidatesV1(group, target, ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}}, nil)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("%s selection: %v %v", name, candidates, err)
		}
		payload := embeddedRecordV1[*PayloadRecordV1](t, "tool:"+name+"/releases/"+version+"/payloads/"+name+"-linux-amd64")
		if payload.Kind != "executable-archive" || payload.SymbolicLinkPolicy != "reject" || len(payload.Executables) != 1 {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		source := embeddedRecordV1[*ArtifactSourceRecordV1](t, "tool:"+name+"/releases/"+version+"/revisions/1/sources/linux-amd64")
		if source.SHA256 != payload.SHA256 || source.SHA256 == canonical.Digest("") || len(source.Mirrors) != 1 {
			t.Fatalf("unbound acquisition: %#v", source)
		}
		target.Platform = "linux/arm64"
		target.OCIArchitecture = "arm64"
		target.NativeArchitecture = "arm64"
		if _, err = catalog.SelectReleaseCandidatesV1(group, target, ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}}, nil); err == nil {
			t.Fatalf("%s silently accepted unqualified ARM64", name)
		}
	}
}
