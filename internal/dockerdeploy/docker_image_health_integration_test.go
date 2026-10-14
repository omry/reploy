package dockerdeploy

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

// A caller supplies three owned images on its disposable compatible endpoint.
// This test creates no resources and never chooses a different endpoint.
func TestImageHealthCompatibleEndpointIntegration(t *testing.T) {
	if os.Getenv("REPLOY_IMAGE_HEALTH_INTEGRATION") != "1" {
		t.Skip("requires disposable healthcheck image fixtures")
	}
	var fixtures map[string]string
	if err := json.Unmarshal([]byte(os.Getenv("REPLOY_IMAGE_HEALTH_FIXTURES")), &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 3 || fixtures["absent"] == "" || fixtures["disabled"] == "" || fixtures["active"] == "" {
		t.Fatal("requires absent, disabled and active immutable IDs")
	}
	ctx, cancel := context.WithTimeout(withGeneratedBuilderSession(t.Context()), imageHealthExportTimeout)
	defer cancel()
	builder, err := selectGeneratedImageBuilder(ctx, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if builder.mode != generatedPodmanAPI {
		t.Fatal("disposable endpoint is not Podman")
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	none, err := canonicalizeDockerHealthcheck(dockerHealthcheck{Test: []string{"NONE"}})
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range fixtures {
		start := time.Now()
		for attempt := 0; attempt < 2; attempt++ {
			image, err := inspectBuiltImageCandidate(ctx, BuiltImageCandidate{ImageID: canonical.Digest(id), builder: builder}, platform, runDockerOutput)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "disabled":
				if image.Config.Healthcheck != none {
					t.Fatalf("disabled metadata: %s", image.Config.Healthcheck)
				}
			case "absent":
				if image.Config.Healthcheck != "" {
					t.Fatalf("invented absent healthcheck: %s", image.Config.Healthcheck)
				}
			case "active":
				if image.Config.Healthcheck == "" || image.Config.Healthcheck == none {
					t.Fatalf("lost active healthcheck: %s", image.Config.Healthcheck)
				}
			}
			t.Logf("%s attempt %d: %s", name, attempt+1, time.Since(start))
			start = time.Now()
		}
	}
}
