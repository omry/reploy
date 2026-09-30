package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func TestRemoveRecordedGenerationReferencesV1(t *testing.T) {
	for _, test := range []struct {
		name         string
		portable     bool
		finalError   error
		removeError  error
		restoreError error
		want         []string
		wantError    string
	}{
		{name: "ordinary", want: []string{"final"}},
		{name: "portable", portable: true, want: []string{"portable", "final"}},
		{name: "portable removal fails", portable: true, removeError: errors.New("portable unavailable"), want: []string{"portable"}, wantError: "portable unavailable"},
		{name: "final removal fails and restores portable", portable: true, finalError: errors.New("final unavailable"), want: []string{"portable", "final", "restore"}, wantError: "final unavailable"},
		{name: "restoration failure is reported", portable: true, finalError: errors.New("final unavailable"), restoreError: errors.New("restore unavailable"), want: []string{"portable", "final", "restore"}, wantError: "restore unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			lock := deploy.BuildLockV1{}
			if test.portable {
				lock.PortableRuntimeLayer = &deploy.PortableRuntimeLayerV1{}
			}
			order := []string{}
			check := func(generation, environment, dir string) {
				if generation != "generation" || environment != "demo" || dir != "/deployment" {
					t.Fatalf("reference identity = %q/%q/%q", generation, environment, dir)
				}
			}
			err := removeRecordedGenerationReferencesV1(t.Context(), lock, "generation", "demo", "/deployment",
				func(_ context.Context, _ providers.RealizedImageV1, generation, environment, dir string) error {
					check(generation, environment, dir)
					order = append(order, "final")
					return test.finalError
				},
				func(_ context.Context, _ providers.RealizedImageV1, generation, environment, dir string) error {
					check(generation, environment, dir)
					order = append(order, "portable")
					return test.removeError
				},
				func(_ context.Context, _ providers.RealizedImageV1, generation, environment, dir string) error {
					check(generation, environment, dir)
					order = append(order, "restore")
					return test.restoreError
				},
			)
			if !reflect.DeepEqual(order, test.want) {
				t.Fatalf("reference operations = %v, want %v", order, test.want)
			}
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestRemoveRecordedGenerationReferencesV1RequiresPortableBackend(t *testing.T) {
	lock := deploy.BuildLockV1{PortableRuntimeLayer: &deploy.PortableRuntimeLayerV1{}}
	called := false
	err := removeRecordedGenerationReferencesV1(t.Context(), lock, "generation", "demo", "/deployment",
		func(context.Context, providers.RealizedImageV1, string, string, string) error {
			called = true
			return nil
		}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "portable-layer reference support") || called {
		t.Fatalf("missing portable backend: error = %v, final removal called = %t", err, called)
	}
}
