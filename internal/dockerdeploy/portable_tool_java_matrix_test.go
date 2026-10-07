package dockerdeploy

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/toolcatalog"
)

func TestPortableToolJavaMatrixSelectsEveryAdvertisedCase(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForToolV1(cases, "java")
	if err != nil {
		t.Fatal(err)
	}
	advertised := 0
	for _, c := range cases {
		if c.Manifest.Tool == "java" {
			advertised++
		}
	}
	if len(selected) != advertised || advertised != 4 {
		t.Fatalf("selected %d of %d advertised Java cases; accepted matrix has four", len(selected), advertised)
	}
	tuples := map[string]bool{}
	for _, c := range selected {
		if c.Manifest.Tool != "java" || c.Manifest.Version != "21" || c.Support.Context != "build" || c.Fixture.Target.Platform != "linux/amd64" {
			t.Fatalf("outside accepted Java scope: %#v", c)
		}
		tuples[c.Target.Target.OSReleaseID+"/"+c.Target.Target.VersionID] = true
	}
	if !reflect.DeepEqual(tuples, map[string]bool{"debian/12": true, "debian/13": true, "ubuntu/25.10": true, "ubuntu/26.04": true}) {
		t.Fatalf("accepted Java target coverage changed: %v", tuples)
	}
	for i, j := 0, len(cases)-1; i < j; i, j = i+1, j-1 {
		cases[i], cases[j] = cases[j], cases[i]
	}
	again, err := portableToolIntegrationCasesForToolV1(cases, "java")
	if err != nil || !reflect.DeepEqual(selected, again) {
		t.Fatalf("catalog order changed the exhaustive Java set: %v", err)
	}
}

func TestPortableToolJavaMatrixRejectsInvalidInventory(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForToolV1(cases, "java")
	if err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string][]toolcatalog.IntegrationCaseV1{
		"empty": nil, "duplicate": {selected[0], selected[0]},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := portableToolIntegrationCasesForToolV1(set, "java"); err == nil {
				t.Fatal("invalid Java inventory accepted")
			}
		})
	}
	if _, err := portableToolIntegrationCasesForToolV1(cases, "unknown"); err == nil {
		t.Fatal("unadvertised tool accepted")
	}
	for name, mutate := range map[string]func(*toolcatalog.IntegrationCaseV1){
		"runtime": func(c *toolcatalog.IntegrationCaseV1) { c.Support.Context = "runtime" },
		"arm64":   func(c *toolcatalog.IntegrationCaseV1) { c.Fixture.Target.Platform = "linux/arm64" },
		"fixture": func(c *toolcatalog.IntegrationCaseV1) { c.Fixture.BaseImageDigest = rendererDigest("f") },
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(selected)
			if err != nil {
				t.Fatal(err)
			}
			var changed []toolcatalog.IntegrationCaseV1
			if err := json.Unmarshal(data, &changed); err != nil {
				t.Fatal(err)
			}
			mutate(&changed[len(changed)-1])
			if _, err := portableToolIntegrationCasesForToolV1(changed, "java"); err == nil {
				t.Fatal("unauthenticated Java inventory accepted")
			}
		})
	}
}
