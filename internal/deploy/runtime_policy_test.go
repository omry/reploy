package deploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
)

func validRuntimePolicy() RuntimePolicyV1 {
	return RuntimePolicyV1{
		Schema: RuntimePolicySchemaV1,
		ProtectedPaths: []ProtectedPathV1{
			{Path: "/.reploy", Kind: ProtectedPathReployRoot, Owner: "reploy"},
			{Path: "/opt/app/bin/tool", Kind: ProtectedPathExecutablePath, Owner: "app.tool"},
		},
		Plans: []RuntimePlanV1{{
			ID: "serve",
			Mounts: []RuntimeMountV1{
				{Destination: "/mnt/config", SourceKind: RuntimeMountSourceFile, ReadOnly: true},
				{Destination: "/workspace/output", SourceKind: RuntimeMountSourceDirectory},
			},
			Executables: []providers.QualifiedOutput{{Component: "app", Name: "serve"}},
		}},
	}
}

func TestRuntimePolicyDigestV1IsStable(t *testing.T) {
	policy := validRuntimePolicy()
	first, err := RuntimePolicyDigestV1(policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RuntimePolicyDigestV1(policy)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == "" {
		t.Fatalf("runtime policy digests = %q, %q", first, second)
	}
}

func TestValidateRuntimePolicyV1RejectsNoncanonicalStructure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RuntimePolicyV1)
		want   string
	}{
		{name: "nil protected paths", mutate: func(value *RuntimePolicyV1) { value.ProtectedPaths = nil }, want: "collections"},
		{name: "unsorted protected", mutate: func(value *RuntimePolicyV1) {
			value.ProtectedPaths[0], value.ProtectedPaths[1] = value.ProtectedPaths[1], value.ProtectedPaths[0]
		}, want: "protected paths"},
		{name: "unsafe owner", mutate: func(value *RuntimePolicyV1) { value.ProtectedPaths[0].Owner = "\n" }, want: "owner"},
		{name: "nil mounts", mutate: func(value *RuntimePolicyV1) { value.Plans[0].Mounts = nil }, want: "collections"},
		{name: "unsorted mounts", mutate: func(value *RuntimePolicyV1) {
			value.Plans[0].Mounts[0], value.Plans[0].Mounts[1] = value.Plans[0].Mounts[1], value.Plans[0].Mounts[0]
		}, want: "mounts"},
		{name: "host-like destination", mutate: func(value *RuntimePolicyV1) { value.Plans[0].Mounts[0].Destination = `C:\config` }, want: "absolute Linux path"},
		{name: "filesystem root", mutate: func(value *RuntimePolicyV1) { value.Plans[0].Mounts[0].Destination = "/" }, want: "filesystem root"},
		{name: "kernel subtree", mutate: func(value *RuntimePolicyV1) { value.Plans[0].Mounts[0].Destination = "/proc/app" }, want: `reserved container path "/proc"`},
		{name: "Docker resolver parent", mutate: func(value *RuntimePolicyV1) { value.Plans[0].Mounts[0].Destination = "/etc" }, want: `reserved container path "/etc/hostname"`},
		{name: "source kind", mutate: func(value *RuntimePolicyV1) { value.Plans[0].Mounts[0].SourceKind = "bind" }, want: "source kind"},
		{name: "duplicate executable", mutate: func(value *RuntimePolicyV1) {
			value.Plans[0].Executables = append(value.Plans[0].Executables, value.Plans[0].Executables[0])
		}, want: "executables"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := validRuntimePolicy()
			test.mutate(&policy)
			err := ValidateRuntimePolicyV1(policy)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}
