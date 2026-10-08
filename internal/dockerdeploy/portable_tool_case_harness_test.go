package dockerdeploy

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func TestPortableToolBashAMD64CasesDeriveCompleteCurrentInventory(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForPlatformV1(cases, "bash", "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	want := map[canonical.Digest]bool{}
	for _, c := range cases {
		if c.Manifest.Tool == "bash" && c.Target.Target.OCIArchitecture == "amd64" {
			want[c.ID] = true
		}
	}
	contexts := map[string]map[string]bool{}
	for _, c := range selected {
		if !want[c.ID] || c.Fixture.Target.Platform != "linux/amd64" {
			t.Fatal("unadvertised or other-architecture case selected")
		}
		delete(want, c.ID)
		key := c.Target.Target.OSReleaseID + "/" + c.Target.Target.VersionID
		if contexts[key] == nil {
			contexts[key] = map[string]bool{}
		}
		contexts[key][c.Support.Context] = true
	}
	if len(selected) != 6 || len(want) != 0 || len(contexts) != 3 {
		t.Fatalf("incomplete current Bash amd64 inventory: cases=%d missing=%d targets=%d", len(selected), len(want), len(contexts))
	}
	for target, contexts := range contexts {
		if !contexts["build"] || !contexts["runtime"] || len(contexts) != 2 {
			t.Fatalf("target %s lacks both exact contexts", target)
		}
	}
	for i, j := 0, len(cases)-1; i < j; i, j = i+1, j-1 {
		cases[i], cases[j] = cases[j], cases[i]
	}
	again, err := portableToolIntegrationCasesForPlatformV1(cases, "bash", "linux/amd64")
	if err != nil || !reflect.DeepEqual(selected, again) {
		t.Fatalf("inventory order changed the exhaustive set: %v", err)
	}
	if _, err := portableToolIntegrationCasesForPlatformV1(cases, "bash", "linux/unsupported"); err == nil {
		t.Fatal("empty or unsupported architecture set accepted")
	}
}

func TestPortableToolRepresentativesDeriveFromCurrentInventory(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := representativePortableToolCasesV1(cases)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(cases)-1; i < j; i, j = i+1, j-1 {
		cases[i], cases[j] = cases[j], cases[i]
	}
	again, err := representativePortableToolCasesV1(cases)
	if err != nil || !reflect.DeepEqual(selected, again) {
		t.Fatalf("inventory order changed representatives: %v", err)
	}
	contexts := map[string]bool{}
	for _, caseV1 := range selected {
		contexts[caseV1.Support.Context] = true
		document, err := portableToolIntegrationDocumentV1(caseV1)
		if err != nil {
			t.Fatal(err)
		}
		if document.Environment.Base.Image != portableToolIntegrationBaseV1(caseV1) {
			t.Fatal("mutable fixture base")
		}
		app := document.Environment.Applications["case"]
		if caseV1.Support.Context == "runtime" {
			if len(app.Packages.Tools) != 1 {
				t.Fatal("runtime case lost its ordinary request")
			}
			group := app.Packages.Tools[0]
			if group.Scope != "application:case" || group.Tool != caseV1.Manifest.Tool ||
				group.DefinitionRevision != caseV1.Manifest.Revision || !reflect.DeepEqual(group.Selections, caseV1.Support.Selections) ||
				!reflect.DeepEqual(group.Binding.Explicit, caseV1.Support.Bindings) {
				t.Fatalf("case request changed: %#v", group)
			}
		} else if len(app.Packages.Tools) != 0 {
			t.Fatal("build tool leaked into application request")
		}
	}
	if len(selected) != len(contexts) || !contexts["build"] || !contexts["runtime"] || len(selected) >= len(cases) {
		t.Fatal("representative suite claims the full inventory")
	}
	if _, err := representativePortableToolCasesV1(nil); err == nil {
		t.Fatal("empty inventory accepted")
	}
}

func TestPortableToolHarnessUnsupportedRequestsFailBeforeAcquisition(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	for _, caseV1 := range cases {
		faults := []string{"target", "architecture", "binding", "selection"}
		canRuntime := false
		for _, other := range cases {
			canRuntime = canRuntime || (other.ManifestReference == caseV1.ManifestReference && other.Target.Target == caseV1.Target.Target && other.Support.Context == "runtime")
		}
		if caseV1.Support.Context == "build" && !canRuntime {
			faults = []string{"context"}
		}
		for _, fault := range faults {
			t.Run(caseV1.Fixture.ID+"/"+fault, func(t *testing.T) {
				// Translate the same definition-derived request through ordinary
				// application planning. A build-only tool must reject runtime use.
				requested := caseV1
				requested.Support.Context = "runtime"
				document, err := portableToolIntegrationDocumentV1(requested)
				if err != nil {
					t.Fatal(err)
				}
				app := document.Environment.Applications["case"]
				groups := app.Packages.Tools
				app.Packages.Tools = nil
				document.Environment.Applications["case"] = app
				base := testProbeImageDescriptor(t, caseV1.Fixture.Target.Platform)
				ordinary, err := BuildResolvedRequestV1(document, deploy.EmptyRequestOverlayV1(), base.Platform, []providers.ResolvedSourceInput{})
				if err != nil {
					t.Fatal(err)
				}
				app.Packages.Tools = groups
				target := caseV1.Target.Target
				switch fault {
				case "target":
					target.VersionID = "unknown"
				case "architecture":
					if target.OCIArchitecture == "arm64" {
						target.Platform, target.OCIArchitecture, target.NativeArchitecture = "linux/amd64", "amd64", "amd64"
					} else {
						target.Platform, target.OCIArchitecture, target.NativeArchitecture = "linux/arm64", "arm64", "arm64"
					}
				case "binding":
					app.Packages.Tools[0].Binding.Explicit = []string{"unknown"}
				case "selection":
					app.Packages.Tools[0].Selections = map[string][]string{"browser": {"unknown"}}
				}
				document.Environment.Applications["case"] = app
				previous := observeApplicationPortableTargetV1
				observeApplicationPortableTargetV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor) (toolcatalog.TargetIdentityV1, error) {
					return target, nil
				}
				t.Cleanup(func() { observeApplicationPortableTargetV1 = previous })
				store, err := providerstore.NewStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if plan, err := PlanApplicationPortableToolsV1(t.Context(), PlanApplicationPortableToolsInputV1{
					Document: document, Components: ordinary.Components, Platform: base.Platform, Base: base,
					Store: store, ReployVersion: deploy.ToolVersion, FinalImageConfig: pythonConsumerTestImageConfig(),
				}); err == nil || plan != nil {
					t.Fatalf("unsupported %s planned: %v", fault, err)
				}
				if _, err := os.Lstat(store.Root()); !os.IsNotExist(err) {
					t.Fatalf("unsupported request acquired artifacts or created storage: %v", err)
				}
			})
		}
	}
}

func TestPortableToolHarnessPreflightsTheCompleteSet(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := portableToolIntegrationRequestsV1(cases); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*toolcatalog.IntegrationCaseV1){
		"context":      func(c *toolcatalog.IntegrationCaseV1) { c.Support.Context = "unsupported" },
		"target":       func(c *toolcatalog.IntegrationCaseV1) { c.Target.Target.VersionID = "unknown" },
		"architecture": func(c *toolcatalog.IntegrationCaseV1) { c.Fixture.Target.Platform = "linux/arm64" },
		"binding":      func(c *toolcatalog.IntegrationCaseV1) { c.Support.Bindings = []string{"unknown"} },
		"selection": func(c *toolcatalog.IntegrationCaseV1) {
			c.Support.Selections = map[string][]string{"browser": {"unknown"}}
		},
		"fixture": func(c *toolcatalog.IntegrationCaseV1) {
			c.Fixture.BaseImageDigest = rendererDigest("f")
		},
		"profiles": func(c *toolcatalog.IntegrationCaseV1) { c.Profiles = nil },
		"identity": func(c *toolcatalog.IntegrationCaseV1) { c.ID = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			// Put the invalid case last: preflight must reject before any earlier
			// valid case is executed or its artifact acquired.
			encoded, err := json.Marshal(cases[len(cases)-1])
			if err != nil {
				t.Fatal(err)
			}
			var changed toolcatalog.IntegrationCaseV1
			if err := json.Unmarshal(encoded, &changed); err != nil {
				t.Fatal(err)
			}
			mutate(&changed)
			set := append(append([]toolcatalog.IntegrationCaseV1(nil), cases[:len(cases)-1]...), changed)
			if _, err := portableToolIntegrationRequestsV1(set); err == nil {
				t.Fatal("invalid selected set admitted")
			}
		})
	}
	for _, set := range [][]toolcatalog.IntegrationCaseV1{nil, {cases[0], cases[0]}} {
		if _, err := portableToolIntegrationRequestsV1(set); err == nil {
			t.Fatal("empty or duplicate exercised set accepted")
		}
	}
}

func TestPortableToolHarnessFailedCaseCannotPassExternalGate(t *testing.T) {
	for _, fault := range []string{"missing callback", "failed profile"} {
		t.Run(fault, func(t *testing.T) {
			input, caseV1, lock, image := applicationCaseFixtureV1(t)
			outcome := PortableToolProbeOutcomePassV1
			if fault == "failed profile" {
				outcome = PortableToolProbeOutcomeExitV1
			}
			calls := stubCaseEvidenceExecutorV1(t, outcome, nil)
			observation, err := observeApplicationBuildCaseV1(t.Context(), input, caseV1, "application:demo",
				func(ctx context.Context, got LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
					if fault != "missing callback" {
						if err := got.observeFinalImage(ctx, image, lock); err != nil {
							return LockedProviderBuildExecutionResultV1{}, err
						}
					}
					return LockedProviderBuildExecutionResultV1{Lock: lock}, nil
				})
			if err == nil {
				t.Fatal("failed case returned success")
			}
			if fault == "missing callback" && *calls != 0 {
				t.Fatal("probe ran without the owned image callback")
			}
			store, err := providerstore.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := PersistPortableToolCaseEvidenceV1(t.Context(), store, observation); err == nil {
				t.Fatal("failed case published passing evidence")
			}
			if err := RequireCurrentPortableToolCaseEvidenceV1(store, []PortableToolCaseEvidenceRequestV1{{Case: caseV1, Scope: "application:demo"}}, map[canonical.Digest]providerstore.StoreObjectRef{}); err == nil {
				t.Fatal("failed case passed exercised-set gate")
			}
		})
	}
}
