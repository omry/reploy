package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"reflect"
	"strings"
	"testing"
)

func projectionDocumentV1(t *testing.T, target string) blueprint.Document {
	t.Helper()
	policy, _ := identitySourcePolicyV1(t)
	d := identityDestinationDocumentV1(policy)
	m := blueprint.EnvironmentMount{Target: target, Writable: true, UpdatePolicy: blueprint.UpdatePreserve}
	d.Environment.Mounts = map[string]blueprint.EnvironmentMount{"data": m}
	d.Docker.Mounts = map[string]blueprint.DockerMount{"data": {Extends: "environment.mounts.data", Mode: blueprint.MountManagedBind, Source: "data", Contract: m}}
	return d
}
func TestImageDestinationProjectionV1LayerControls(t *testing.T) {
	dir := func(p string) healthArchiveMember { return healthArchiveMember{name: p, kind: tar.TypeDir} }
	file := func(p string) healthArchiveMember {
		return healthArchiveMember{name: p, content: []byte("image-data-or-code")}
	}
	link := func(p, to string) healthArchiveMember {
		return healthArchiveMember{name: p, kind: tar.TypeSymlink, linkname: to}
	}
	cases := []struct {
		name, target   string
		layers         [][]healthArchiveMember
		empty, invalid bool
	}{
		{"absent", "/var/lib/mail", nil, true, false},
		{"empty", "/var/lib/mail", [][]healthArchiveMember{{dir("var/lib/mail")}}, true, false},
		{"directories-only", "/var/lib/mail", [][]healthArchiveMember{{dir("var/lib/mail/queue")}}, true, false},
		{"data-is-protected-too", "/var/lib/mail", [][]healthArchiveMember{{file("var/lib/mail/default.conf")}}, false, false},
		{"zero-size-file", "/var/lib/mail", [][]healthArchiveMember{{{name: "var/lib/mail/empty"}}}, false, false},
		{"prefix-is-not-overlap", "/var/lib/mail", [][]healthArchiveMember{{file("var/lib/mailbox/code")}}, true, false},
		{"whiteout-removes-lower", "/var/lib/mail", [][]healthArchiveMember{{file("var/lib/mail/default.conf")}, {{name: "var/lib/mail/.wh.default.conf"}}}, true, false},
		{"whiteout-keeps-same-layer", "/var/lib/mail", [][]healthArchiveMember{{file("var/lib/mail/default.conf"), {name: "var/lib/mail/.wh.default.conf"}}}, false, false},
		{"opaque-removes-lower", "/var/lib/mail", [][]healthArchiveMember{{file("var/lib/mail/queue/code")}, {{name: "var/lib/mail/.wh..wh..opq"}}}, true, false},
		{"opaque-keeps-same-layer", "/var/lib/mail", [][]healthArchiveMember{{file("var/lib/mail/old")}, {file("var/lib/mail/new"), {name: "var/lib/mail/.wh..wh..opq"}}}, false, false},
		{"absolute-image-alias", "/data", [][]healthArchiveMember{{file("var/lib/mail/code"), link("data", "/var/lib/mail")}}, false, false},
		{"relative-image-alias", "/var/run/mail", [][]healthArchiveMember{{dir("run/mail"), link("var/run", "../run")}}, true, false},
		{"alias-to-populated-tree", "/var/run/mail", [][]healthArchiveMember{{file("run/mail/code"), link("var/run", "../run")}}, false, false},
		{"alias-cycle", "/a", [][]healthArchiveMember{{link("a", "b"), link("b", "a")}}, false, true},
		{"file-destination", "/data", [][]healthArchiveMember{{file("data")}}, false, true},
		{"layer-traverses-link", "/data", [][]healthArchiveMember{{link("alias", "data")}, {file("alias/code")}}, false, true},
		{"alias-to-root", "/data", [][]healthArchiveMember{{link("data", "/")}}, false, true},
		{"hardlink-is-file", "/data", [][]healthArchiveMember{{file("original"), {name: "data/alias", kind: tar.TypeLink, linkname: "original"}}}, false, false},
		{"link-descendant-protects-subtree", "/data", [][]healthArchiveMember{{dir("data"), link("data/alias", "/outside")}}, false, false},
		{"file-replaced-by-directory", "/data", [][]healthArchiveMember{{file("data")}, {dir("data")}}, true, false},
		{"directory-replaced-by-file", "/data", [][]healthArchiveMember{{file("data/old")}, {file("data")}}, false, true},
		{"intrinsic-root-not-data", "/", nil, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layers := [][]byte{healthArchive(t, identitySourceMembersV1()...)}
			for _, members := range tc.layers {
				layers = append(layers, healthArchive(t, members...))
			}
			image, archive := identitySourceArchiveV1(t, layers, nil)
			_, platform := identitySourcePolicyV1(t)
			document := projectionDocumentV1(t, tc.target)
			result, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), document, image, platform)
			if (err != nil) != tc.invalid {
				t.Fatalf("invalid=%v err=%v", tc.invalid, err)
			}
			if err == nil {
				if result.Destinations == nil || len(result.Destinations.Destinations) != 1 {
					t.Fatal("projection missing")
				}
				fact := result.Destinations.Destinations[0]
				if fact.ContainsContent == tc.empty {
					t.Fatalf("content=%v wanted empty=%v", fact.ContainsContent, tc.empty)
				}
				lock := deploy.BuildLockV1{FinalImage: image, Platform: platform, ImageDestinations: result.Destinations}
				if err := deploy.ValidateSelectedImageDestinationsV1(document, lock); err != nil {
					t.Fatal(err)
				}
			} else if !reflect.DeepEqual(result, preparedImageEvidenceV1{}) {
				t.Fatal("partial evidence leaked")
			}
			image.RootFSSubject = canonical.Digest("sha256:" + strings.Repeat("a", 64))
			if _, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), document, image, platform); err == nil {
				t.Fatal("unbound rootfs accepted")
			}
		})
	}
}
func TestImageDestinationProjectionV1FileBindCompatibility(t *testing.T) {
	for _, writable := range []bool{false, true} {
		for _, target := range []string{"/data", "/data/child"} {
			t.Run(fmt.Sprintf("writable=%t/target=%s", writable, target), func(t *testing.T) {
				source, err := blueprint.Decode([]byte(fmt.Sprintf(`blueprint: {schema: 1, version: 0.1.0, requires_reploy: ">=NEXT", compatibility: {platforms: [linux/amd64]}}
environment:
  id: file-bind-probe
  base: {image: python:3.13-slim}
  runtime:
    identity_policy:
      schema: 1
      groups: {worker: 200, shared: 300, unused: 400}
      users: {worker: {uid: 200, supplementary_groups: [shared]}}
      roles: {app: {user: worker}}
  mounts:
    data: {target: %s, writable: %t, update_policy: unmanaged}
docker:
  mounts:
    data: {extends: environment.mounts.data, mode: bind, source: /provided/config}
`, target, writable)))
				if err != nil {
					t.Fatal(err)
				}
				document, err := blueprint.Resolve(source)
				if err != nil {
					t.Fatal(err)
				}
				members := append(identitySourceMembersV1(), healthArchiveMember{name: "data", content: []byte("existing-image-config")})
				image, archive := identitySourceArchiveV1(t, [][]byte{healthArchive(t, members...)}, nil)
				_, platform := identitySourcePolicyV1(t)
				result, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), document, image, platform)
				if writable || target != "/data" {
					if err == nil || !reflect.DeepEqual(result, preparedImageEvidenceV1{}) {
						t.Fatal("file ancestor or writable file destination accepted", err)
					}
					return
				}
				if err != nil {
					t.Fatal("approved read-only file bind rejected", err)
				}
				fact := result.Destinations.Destinations[0]
				if fact.Kind != "non-directory" || !fact.ContainsContent || fact.Target != target || fact.Resolved != target {
					t.Fatalf("incorrect retained file facts: %#v", fact)
				}
				if err := deploy.ValidateSelectedImageDestinationsV1(document, deploy.BuildLockV1{FinalImage: image, Platform: platform, ImageDestinations: result.Destinations}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestImageDestinationProjectionV1Cancellation(t *testing.T) {
	image, archive := identitySourceArchiveV1(t, [][]byte{healthArchive(t, identitySourceMembersV1()...)}, nil)
	_, platform := identitySourcePolicyV1(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := readDockerPreparedImageArchiveV1(ctx, bytes.NewReader(archive), projectionDocumentV1(t, "/data"), image, platform)
	if err == nil || !reflect.DeepEqual(result, preparedImageEvidenceV1{}) {
		t.Fatal("cancelled reader published evidence")
	}
	index := imageDestinationIndexV1{"data": {kind: tar.TypeDir}}
	if _, _, _, err := index.destination(ctx, "/data"); err == nil {
		t.Fatal("cancelled projection accepted")
	}
	if err := index.apply(ctx, []imageDestinationNodeV1{{name: "data", kind: tar.TypeDir}}, &imageDestinationBudgetV1{}); err == nil {
		t.Fatal("cancelled layer accepted")
	}
}

func TestImageDestinationProjectionV1RetainedBudget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget imageDestinationBudgetV1
		entry  healthArchiveMember
	}{
		{"node", imageDestinationBudgetV1{nodes: imageDestinationNodeLimitV1}, healthArchiveMember{name: "data"}},
		{"path-bytes", imageDestinationBudgetV1{bytes: imageDestinationMetadataLimitV1 - 3}, healthArchiveMember{name: "data"}},
		{"link-bytes", imageDestinationBudgetV1{bytes: imageDestinationMetadataLimitV1 - 5}, healthArchiveMember{name: "data", kind: tar.TypeSymlink, linkname: "long-destination"}},
		{"implicit-parent", imageDestinationBudgetV1{nodes: imageDestinationNodeLimitV1 - 1}, healthArchiveMember{name: "parent/data"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layers := &imageIdentityLayersV1{projectionBudget: &tc.budget}
			err := layers.add(t.Context(), "layer.tar", bytes.NewReader(healthArchive(t, tc.entry)))
			if err == nil {
				err = imageDestinationIndexV1{}.apply(t.Context(), layers.layers["layer.tar"].entries, layers.projectionBudget)
			}
			if err == nil {
				t.Fatal("retained metadata limit was not enforced")
			}
		})
	}
	// Both accounting fields reject before mutation, including the exact boundary.
	budget := imageDestinationBudgetV1{nodes: imageDestinationNodeLimitV1 - 1, bytes: imageDestinationMetadataLimitV1 - 4}
	if _, err := budget.entry(&tar.Header{Typeflag: tar.TypeDir}, "data"); err != nil {
		t.Fatal(err)
	}
	before := budget
	if _, err := budget.entry(&tar.Header{Typeflag: tar.TypeDir}, "x"); err == nil || budget != before {
		t.Fatal("failed allocation changed accounting")
	}
}

func TestImageDestinationProjectionV1PhysicalMetadataCharge(t *testing.T) {
	raw := healthArchive(t, healthArchiveMember{name: strings.Repeat("x", 200), format: tar.FormatPAX})
	if raw[156] != tar.TypeXHeader {
		t.Fatal("fixture lacks extension header")
	}
	budget := &imageDestinationBudgetV1{bytes: imageDestinationMetadataLimitV1 - 512}
	layers := &imageIdentityLayersV1{projectionBudget: budget}
	if err := layers.add(t.Context(), "layer.tar", bytes.NewReader(raw)); err == nil {
		t.Fatal("hidden extension metadata was not charged")
	}
	if len(layers.layers) != 0 {
		t.Fatal("failed layer published partial entries")
	}
}

func TestImageDestinationProjectionV1MalformedLayers(t *testing.T) {
	for _, entry := range []healthArchiveMember{
		{name: "data/.wh."}, {name: "data/.wh.."}, {name: "data/.wh..."},
		{name: "data/.wh.file", content: []byte("not-empty")},
		{name: "data/.wh.file", kind: tar.TypeSymlink, linkname: "elsewhere"},
		{name: "data", kind: tar.TypeSymlink, linkname: strings.Repeat("x", 4097)},
		{name: "data", kind: tar.TypeSymlink, linkname: ""},
	} {
		t.Run(entry.name+entry.linkname[:min(len(entry.linkname), 8)], func(t *testing.T) {
			image, archive := identitySourceArchiveV1(t, [][]byte{healthArchive(t, identitySourceMembersV1()...), healthArchive(t, entry)}, nil)
			_, platform := identitySourcePolicyV1(t)
			if _, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), projectionDocumentV1(t, "/data"), image, platform); err == nil {
				t.Fatal("malformed projection source accepted")
			}
		})
	}
}

func TestImageDestinationProjectionV1AliasedFactsAndDeterminism(t *testing.T) {
	base := healthArchive(t, append(identitySourceMembersV1(), healthArchiveMember{name: "run/mail", kind: tar.TypeDir}, healthArchiveMember{name: "var/run", kind: tar.TypeSymlink, linkname: "../run"})...)
	image, archive := identitySourceArchiveV1(t, [][]byte{base}, nil)
	_, platform := identitySourcePolicyV1(t)
	document := projectionDocumentV1(t, "/var/run/mail")
	first, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), document, image, platform)
	if err != nil {
		t.Fatal(err)
	}
	fact := first.Destinations.Destinations[0]
	if fact.Target != "/var/run/mail" || fact.Resolved != "/run/mail" || fact.Kind != "directory" || fact.ContainsContent {
		t.Fatalf("bad alias facts: %#v", fact)
	}
	second, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), document, image, platform)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("projection is not deterministic", err)
	}
	document.Environment.Mounts["root"] = blueprint.EnvironmentMount{Target: "/", Writable: true}
	writable, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), document, image, platform)
	if err != nil || !writable.Destinations.RootWritable || writable.Destinations.SelectionDigest == first.Destinations.SelectionDigest {
		t.Fatal("root policy was not bound", err)
	}
}

func TestImageDestinationProjectionV1AliasHopLimit(t *testing.T) {
	for _, hops := range []int{31, 32, 33} {
		t.Run(fmt.Sprint(hops), func(t *testing.T) {
			members := identitySourceMembersV1()
			for i := 0; i < hops; i++ {
				members = append(members, healthArchiveMember{name: fmt.Sprintf("link-%d", i), kind: tar.TypeSymlink, linkname: fmt.Sprintf("link-%d", i+1)})
			}
			members = append(members, healthArchiveMember{name: fmt.Sprintf("link-%d", hops), kind: tar.TypeDir})
			image, archive := identitySourceArchiveV1(t, [][]byte{healthArchive(t, members...)}, nil)
			_, platform := identitySourcePolicyV1(t)
			_, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), projectionDocumentV1(t, "/link-0"), image, platform)
			if (err != nil) != (hops > 32) {
				t.Fatalf("hops=%d err=%v", hops, err)
			}
		})
	}
}

func TestImageDestinationProjectionV1WhiteoutCannotTraverseAlias(t *testing.T) {
	for _, marker := range []string{".wh.file", ".wh..wh..opq"} {
		t.Run(marker, func(t *testing.T) {
			members := append(identitySourceMembersV1(), healthArchiveMember{name: "alias", kind: tar.TypeSymlink, linkname: "outside"})
			image, archive := identitySourceArchiveV1(t, [][]byte{healthArchive(t, members...), healthArchive(t, healthArchiveMember{name: "alias/" + marker})}, nil)
			_, platform := identitySourcePolicyV1(t)
			if _, err := readDockerPreparedImageArchiveV1(t.Context(), bytes.NewReader(archive), projectionDocumentV1(t, "/data"), image, platform); err == nil {
				t.Fatal("ambiguous whiteout traversal accepted")
			}
		})
	}
}
