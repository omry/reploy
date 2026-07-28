package dockerdeploy

import (
	"reflect"
	"testing"
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
