package dockerdeploy

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestEnvironmentStageAndUpdateOwnMaterialization(t *testing.T) {
	previous := materializeStagedEnvironmentForStage
	t.Cleanup(func() { materializeStagedEnvironmentForStage = previous })
	calls := 0
	materializeStagedEnvironmentForStage = func(
		_ context.Context, dir string, pack deploy.AppPack, _ bool, _ io.Writer, _ io.Writer, _ io.Writer, _ time.Duration,
	) ([]UpdateResult, error) {
		calls++
		if pack.Environment == nil {
			t.Fatal("environment materializer received a legacy pack")
		}
		return []UpdateResult{{Path: filepath.Join(dir, "materialized"), Status: deploy.UpdateStatusUpdated}}, nil
	}
	ref, err := deploy.ParsePackRef("file:../../examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "staging")
	results, err := Init(InitOptions{Dir: dir, Pack: ref, MaterializeEnvironment: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !updateResultsContainPath(results, filepath.Join(dir, "materialized")) {
		t.Fatalf("init calls/results = %d/%#v", calls, results)
	}
	results, err = Update(UpdateOptions{Dir: dir, MaterializeEnvironment: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !updateResultsContainPath(results, filepath.Join(dir, "materialized")) {
		t.Fatalf("update calls/results = %d/%#v", calls, results)
	}
}

func updateResultsContainPath(results []UpdateResult, path string) bool {
	for _, result := range results {
		if result.Path == path {
			return true
		}
	}
	return false
}
