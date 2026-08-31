package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestValidateBuildLockV1BindsPortableToolBaseNode(t *testing.T) {
	newLock := func(t *testing.T) BuildLockV1 {
		t.Helper()
		_, store, lock, _, _ := buildReachabilityFixture(t)
		artifact, err := store.Publish(context.Background(), "portable/demo.tar", "jdk-archive", strings.NewReader("portable"))
		if err != nil {
			t.Fatal(err)
		}
		lock.PortableTools = portableToolReachabilityLockV1(t, &lock, artifact)
		if err := ValidateBuildLockV1(lock, acceptBuildLockProfile); err != nil {
			t.Fatal(err)
		}
		return lock
	}

	tests := []struct {
		name   string
		mutate func(*providers.NodeSpec)
	}{
		{
			name: "base request",
			mutate: func(node *providers.NodeSpec) {
				request, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
					Image: "different-base", Exports: map[string]blueprint.BaseExecutableExport{},
				})
				if err != nil {
					t.Fatal(err)
				}
				*node, err = providers.BaseNodeSpec(request)
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "base requirements",
			mutate: func(node *providers.NodeSpec) {
				node.Requirements.ProviderData.Value = canonical.Object{"image": "different-base", "exports": []any{}}
			},
		},
		{
			name: "base outputs",
			mutate: func(node *providers.NodeSpec) {
				request, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
					Image: "local-base",
					Exports: map[string]blueprint.BaseExecutableExport{
						"python": {Executable: "/usr/bin/python3"},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				*node, err = providers.BaseNodeSpec(request)
				if err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lock := newLock(t)
			baseIndex := -1
			for index := range lock.PortableTools.Plan.ProviderPlan.Nodes {
				if lock.PortableTools.Plan.ProviderPlan.Nodes[index].ID == "base" {
					baseIndex = index
					break
				}
			}
			if baseIndex < 0 {
				t.Fatal("portable tool provider plan has no base node")
			}
			test.mutate(&lock.PortableTools.Plan.ProviderPlan.Nodes[baseIndex])
			if err := ValidateBuildLockV1(lock, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "base plan identity") {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}
