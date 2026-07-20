package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestProbeDockerNativePlatformUsesServerPlatform(t *testing.T) {
	var arguments []string
	platform, err := probeDockerNativePlatform(t.Context(), func(_ context.Context, args ...string) (string, error) {
		arguments = append([]string{}, args...)
		return "linux/amd64\n", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if platform.Canonical != "linux/amd64" {
		t.Fatalf("platform = %q", platform.Canonical)
	}
	wantArguments := []string{"version", "--format", dockerNativePlatformFormat}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %q, want %q", arguments, wantArguments)
	}
}

func TestProbeDockerNativePlatformRejectsCommandAndFormatFailures(t *testing.T) {
	tests := []struct {
		name   string
		output string
		err    error
		want   string
	}{
		{name: "command", err: errors.New("daemon unavailable"), want: "daemon unavailable"},
		{name: "empty", output: "\n", want: "canonical lowercase"},
		{name: "noncanonical", output: "Linux/AMD64\n", want: "lowercase slash-free"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := probeDockerNativePlatform(t.Context(), func(context.Context, ...string) (string, error) {
				return test.output, test.err
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSelectDockerTargetPlatformUsesSingleDeclarationWithoutNativeQuery(t *testing.T) {
	document := targetPlatformDocument(t, "linux/arm64")
	selected, err := SelectDockerTargetPlatform(document, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Canonical != "linux/arm64" {
		t.Fatalf("selected platform = %q", selected.Canonical)
	}
}

func TestSelectDockerTargetPlatformUsesExplicitDeclarationWithoutNativeQuery(t *testing.T) {
	document := targetPlatformDocument(t, "linux/amd64", "linux/arm64")
	selected, err := SelectDockerTargetPlatform(document, "linux/arm64", nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Canonical != "linux/arm64" {
		t.Fatalf("selected platform = %q", selected.Canonical)
	}
}

func TestSelectDockerTargetPlatformUsesUniqueNativeMatch(t *testing.T) {
	document := targetPlatformDocument(t, "linux/amd64", "linux/arm64")
	native := targetPlatform(t, "linux/amd64")
	selected, err := SelectDockerTargetPlatform(document, "", &native)
	if err != nil {
		t.Fatal(err)
	}
	if selected != native {
		t.Fatalf("selected platform = %#v, want %#v", selected, native)
	}
}

func TestSelectDockerTargetPlatformRetainsNativeVariantCoveredByDeclaration(t *testing.T) {
	document := targetPlatformDocument(t, "linux/amd64", "linux/arm")
	native := targetPlatform(t, "linux/arm/v7")
	selected, err := SelectDockerTargetPlatform(document, "", &native)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Canonical != "linux/arm/v7" {
		t.Fatalf("selected platform = %q", selected.Canonical)
	}
}

func TestSelectDockerTargetPlatformRequiresExplicitChoiceWithoutNativeMatch(t *testing.T) {
	document := targetPlatformDocument(t, "linux/amd64", "linux/arm64")
	native := targetPlatform(t, "linux/arm/v7")
	_, err := SelectDockerTargetPlatform(document, "", &native)
	if err == nil || !strings.Contains(err.Error(), "specify --platform") {
		t.Fatalf("error = %v", err)
	}
}

func TestSelectDockerTargetPlatformRejectsUnsupportedOrAmbiguousSelection(t *testing.T) {
	tests := []struct {
		name     string
		document blueprint.Document
		explicit string
		native   *blueprint.Platform
		want     string
	}{
		{
			name:     "explicit platform absent from compatibility",
			document: targetPlatformDocument(t, "linux/amd64", "linux/arm64"),
			explicit: "linux/arm/v7",
			want:     "is not declared",
		},
		{
			name:     "non-Linux Docker target",
			document: targetPlatformDocument(t, "windows/amd64"),
			want:     "require a Linux target",
		},
		{
			name:     "native query required",
			document: targetPlatformDocument(t, "linux/amd64", "linux/arm64"),
			want:     "query the Docker daemon",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := SelectDockerTargetPlatform(test.document, test.explicit, test.native)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func targetPlatformDocument(t *testing.T, values ...string) blueprint.Document {
	t.Helper()
	compatibility, err := blueprint.ParseCompatibility(values)
	if err != nil {
		t.Fatal(err)
	}
	return blueprint.Document{Blueprint: blueprint.Metadata{Compatibility: compatibility}}
}

func targetPlatform(t *testing.T, value string) blueprint.Platform {
	t.Helper()
	platform, err := blueprint.ParsePlatform(value)
	if err != nil {
		t.Fatal(err)
	}
	return platform
}
