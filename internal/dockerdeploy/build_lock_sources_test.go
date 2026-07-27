package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
)

func TestBuildLockSelectedSourcesV1UsesEmbeddedProfilesOnly(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	got, err := buildLockSelectedSourcesV1(fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, fixture.request.SourceCandidates) {
		t.Fatalf("selected sources = %#v, want %#v", got, fixture.request.SourceCandidates)
	}
}

func TestBuildLockSelectedSourceWheelsV1UsesCurrentBundleDescriptors(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	wheels, err := buildLockSelectedSourceWheelsV1(fixture.store, fixture.lock, fixture.request.SourceCandidates)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wheels, fixture.sourceWheels) {
		t.Fatalf("selected source wheels = %#v, want %#v", wheels, fixture.sourceWheels)
	}
	changed := append([]providers.ResolvedSourceInput{}, fixture.request.SourceCandidates...)
	changed[0].ArtifactDigest = reuseTestDigest("f")
	if _, err := buildLockSelectedSourceWheelsV1(fixture.store, fixture.lock, changed); err == nil || !strings.Contains(err.Error(), "not an exact identity") {
		t.Fatalf("changed source error = %v", err)
	}
}
