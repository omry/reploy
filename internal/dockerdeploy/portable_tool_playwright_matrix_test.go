package dockerdeploy

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/toolcatalog"
)

func TestPortableToolPlaywrightMatrixSelectsEveryAdvertisedCase(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForToolV1(cases, "playwright")
	if err != nil {
		t.Fatal(err)
	}
	advertised := 0
	for _, c := range cases {
		if c.Manifest.Tool == "playwright" {
			advertised++
		}
	}
	if len(selected) != advertised || advertised != 3 {
		t.Fatalf("selected %d of %d advertised Playwright cases; accepted matrix has three", len(selected), advertised)
	}
	tuples := map[string]bool{}
	for _, c := range selected {
		if c.Manifest.Version != "1.61.0" || c.Support.Context != "runtime" || c.Fixture.Target.Platform != "linux/amd64" ||
			!reflect.DeepEqual(c.Support.Bindings, []string{"python"}) || !reflect.DeepEqual(c.Support.Selections, map[string][]string{"browser": {"chromium"}}) {
			t.Fatalf("outside accepted Playwright scope: %#v", c)
		}
		tuples[c.Target.Target.OSReleaseID+"/"+c.Target.Target.VersionID] = true
		if len(c.Profiles) != 1 || len(c.Profiles[0].Probes) != 1 {
			t.Fatalf("unexpected Playwright profile schedule: %#v", c.Profiles)
		}
		probe := c.Profiles[0].Probes[0]
		if probe.Path != "/opt/reploy/tools/playwright/bin/playwright" || len(probe.Args) != 7 ||
			!reflect.DeepEqual(probe.Args[:5], []string{"screenshot", "--browser", "chromium", "--wait-for-selector", "#reploy-validation"}) ||
			!strings.HasPrefix(probe.Args[5], "data:text/html,") {
			t.Fatalf("probe does not load and wait for a local HTML page: %#v", probe)
		}
		html, err := url.PathUnescape(strings.TrimPrefix(probe.Args[5], "data:text/html,"))
		if err != nil || !strings.Contains(html, `id="reploy-validation"`) || !strings.Contains(html, "Reploy Playwright validation") {
			t.Fatalf("local page lacks the waited-for content: %q (%v)", html, err)
		}
	}
	if !reflect.DeepEqual(tuples, map[string]bool{"debian/12": true, "ubuntu/25.10": true, "ubuntu/26.04": true}) {
		t.Fatalf("accepted Playwright target coverage changed: %v", tuples)
	}
	for i, j := 0, len(cases)-1; i < j; i, j = i+1, j-1 {
		cases[i], cases[j] = cases[j], cases[i]
	}
	again, err := portableToolIntegrationCasesForToolV1(cases, "playwright")
	if err != nil || !reflect.DeepEqual(selected, again) {
		t.Fatalf("catalog order changed the exhaustive Playwright set: %v", err)
	}
}

func TestPortableToolPlaywrightMatrixRejectsInvalidInventory(t *testing.T) {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForToolV1(cases, "playwright")
	if err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string][]toolcatalog.IntegrationCaseV1{
		"empty": nil, "duplicate": {selected[0], selected[0]},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := portableToolIntegrationCasesForToolV1(set, "playwright"); err == nil {
				t.Fatal("invalid Playwright inventory accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*toolcatalog.IntegrationCaseV1){
		"build":   func(c *toolcatalog.IntegrationCaseV1) { c.Support.Context = "build" },
		"arm64":   func(c *toolcatalog.IntegrationCaseV1) { c.Fixture.Target.Platform = "linux/arm64" },
		"binding": func(c *toolcatalog.IntegrationCaseV1) { c.Support.Bindings = []string{"node"} },
		"browser": func(c *toolcatalog.IntegrationCaseV1) { c.Support.Selections["browser"] = []string{"firefox"} },
		"fixture": func(c *toolcatalog.IntegrationCaseV1) { c.Fixture.BaseImageDigest = rendererDigest("f") },
		"page":    func(c *toolcatalog.IntegrationCaseV1) { c.Profiles[0].Probes[0].Args[5] = "about:blank" },
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
			if _, err := portableToolIntegrationCasesForToolV1(changed, "playwright"); err == nil {
				t.Fatal("unauthenticated Playwright inventory accepted")
			}
		})
	}
}
