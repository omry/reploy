package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func identitySourcePolicyV1(t *testing.T) (*blueprint.IdentityPolicyV1, blueprint.Platform) {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	return &blueprint.IdentityPolicyV1{
		Schema: blueprint.IdentityPolicySchemaV1,
		Groups: map[string]string{"worker": "200", "shared": "300", "unused": "400"},
		Users:  map[string]blueprint.IdentityPolicyUserV1{"worker": {UID: "200", PrimaryGroup: "worker", SupplementaryGroups: []string{"shared"}}},
		Roles:  map[string]blueprint.IdentityPolicyRoleV1{"app": {User: "worker", Capabilities: []string{}}},
	}, platform
}

func identitySourceMembersV1() []healthArchiveMember {
	return []healthArchiveMember{
		{name: "./", kind: tar.TypeDir}, {name: "./etc/", kind: tar.TypeDir},
		{name: "./etc/passwd", content: []byte("worker:SECRET:200:200:PRIVATE:/home/worker:/bin/sh\n")},
		{name: "etc/group", content: []byte("worker:SECRET:200:\nshared:SECRET:300:worker\nunused:SECRET:400:\n")},
		{name: "etc/nsswitch.conf", content: []byte("passwd: files\ngroup: files\n")},
	}
}

type identityArchivePartsV1 struct {
	config, manifest map[string]any
	members          []healthArchiveMember
}

// Outer order deliberately differs from manifest order. The config hash and
// locked rootfs are independent of any later fixture corruption.
func identitySourceArchiveV1(t *testing.T, layers [][]byte, edit func(*identityArchivePartsV1)) (providers.RealizedImageV1, []byte) {
	t.Helper()
	ids, names := []canonical.Digest{}, []string{}
	parts := identityArchivePartsV1{}
	for index, layer := range layers {
		name := fmt.Sprintf("layer-%03d.tar", index)
		ids = append(ids, canonical.Digest(fmtHealthDigest(layer)))
		names = append(names, name)
		parts.members = append([]healthArchiveMember{{name: name, content: layer}}, parts.members...)
	}
	subject, err := deploy.RootFSSubject(ids)
	if err != nil {
		t.Fatal(err)
	}
	parts.config = map[string]any{"os": "linux", "architecture": "amd64", "config": map[string]any{"Env": []string{"SECRET=PRIVATE"}}, "rootfs": map[string]any{"type": "layers", "diff_ids": ids}}
	parts.manifest = map[string]any{"Layers": names}
	if edit != nil {
		edit(&parts)
	}
	config, err := json.Marshal(parts.config)
	if err != nil {
		t.Fatal(err)
	}
	id := canonical.Digest(fmtHealthDigest(config))
	name := strings.TrimPrefix(string(id), "sha256:") + ".json"
	parts.manifest["Config"] = name
	manifest, err := json.Marshal([]map[string]any{parts.manifest})
	if err != nil {
		t.Fatal(err)
	}
	parts.members = append(parts.members, healthArchiveMember{name: name, content: config}, healthArchiveMember{name: "manifest.json", content: manifest})
	return providers.RealizedImageV1{Digest: id, ConfigDigest: id, RootFSSubject: subject}, healthArchive(t, parts.members...)
}

func requireIdentitySourceRejectedV1(t *testing.T, archive []byte, image providers.RealizedImageV1) {
	t.Helper()
	policy, platform := identitySourcePolicyV1(t)
	evidence, err := readDockerImageIdentityArchiveV1(t.Context(), bytes.NewReader(archive), policy, image, platform)
	if err == nil || !reflect.DeepEqual(evidence, deploy.ImageIdentityEvidenceV1{}) {
		t.Fatalf("invalid source accepted: %#v, %v", evidence, err)
	}
	if err.Error() != "image identity archive: invalid or unsupported immutable source" {
		t.Fatalf("source diagnostics were not redacted: %v", err)
	}
}

func TestImageIdentityArchiveV1ManifestOrderedProjection(t *testing.T) {
	policy, platform := identitySourcePolicyV1(t)
	base := healthArchive(t, identitySourceMembersV1()...)
	replacement := []byte("worker:OTHER_SECRET:200:200:NEW_PRIVATE:/different:/bin/false\n")
	top := healthArchive(t,
		healthArchiveMember{name: "etc/passwd", content: replacement},
		healthArchiveMember{name: "unrelated-link", kind: tar.TypeSymlink, linkname: "/etc/passwd"},
		healthArchiveMember{name: "ordinary-hardlink", kind: tar.TypeLink, linkname: "unrelated/file"},
		healthArchiveMember{name: "unrelated/.wh..wh..opq"},
	)
	image, archive := identitySourceArchiveV1(t, [][]byte{base, top}, nil)
	evidence, err := readDockerImageIdentityArchiveV1(t.Context(), bytes.NewReader(archive), policy, image, platform)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Files["etc/passwd"].Digest != canonical.Digest(fmtHealthDigest(replacement)) || evidence.Image != image || evidence.Platform != platform || evidence.Groups["unused"] != "400" {
		t.Fatalf("wrong final source or binding: %#v", evidence)
	}
	encoded, err := deploy.EncodeImageIdentityEvidenceV1(evidence, policy, image, platform)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("SECRET")) || bytes.Contains(encoded, []byte("PRIVATE")) || bytes.Contains(encoded, []byte("/different")) {
		t.Fatal("source data leaked into evidence")
	}
	if _, err := deploy.DecodeImageIdentityEvidenceV1(encoded, policy, image, platform); err != nil {
		t.Fatal(err)
	}
}

func TestImageIdentityArchiveV1RepeatedLayerReferences(t *testing.T) {
	policy, platform := identitySourcePolicyV1(t)
	base := healthArchive(t, identitySourceMembersV1()...)
	replacement := []byte("worker:x:200:200:changed:/different:/bin/false\n")
	top := healthArchive(t, healthArchiveMember{name: "etc/passwd", content: replacement})
	empty := healthArchive(t)
	for _, test := range []struct {
		name   string
		layers [][]byte
		repeat string
	}{
		{"nonempty replacement in order", [][]byte{base, top, base}, "layer-000.tar"},
		{"empty layer", [][]byte{base, empty, empty}, "layer-001.tar"},
	} {
		t.Run(test.name, func(t *testing.T) {
			image, archive := identitySourceArchiveV1(t, test.layers, func(p *identityArchivePartsV1) {
				p.manifest["Layers"].([]string)[2] = test.repeat
				// Store each referenced blob once; the helper reverses outer order.
				p.members = p.members[1:]
			})
			evidence, err := readDockerImageIdentityArchiveV1(t.Context(), bytes.NewReader(archive), policy, image, platform)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.Image != image || evidence.Files["etc/passwd"].Digest != canonical.Digest(fmtHealthDigest(identitySourceMembersV1()[2].content)) {
				t.Fatalf("repeated layer lost ordering or immutable binding: %#v", evidence)
			}
		})
	}
	t.Run("repeated reference with wrong positional digest", func(t *testing.T) {
		image, archive := identitySourceArchiveV1(t, [][]byte{base, top, base}, func(p *identityArchivePartsV1) {
			p.manifest["Layers"].([]string)[2] = "layer-001.tar"
			p.members = p.members[1:]
		})
		requireIdentitySourceRejectedV1(t, archive, image)
	})
	t.Run("duplicate physical member", func(t *testing.T) {
		image, archive := identitySourceArchiveV1(t, [][]byte{base}, func(p *identityArchivePartsV1) {
			p.members = append(p.members, p.members[0])
		})
		requireIdentitySourceRejectedV1(t, archive, image)
	})
}

func TestImageIdentityArchiveV1ImmutableBindings(t *testing.T) {
	base := healthArchive(t, identitySourceMembersV1()...)
	top := healthArchive(t, healthArchiveMember{name: "ordinary", content: []byte("payload")})
	for _, test := range []struct {
		name string
		edit func(*identityArchivePartsV1)
	}{
		{"wrong OS", func(p *identityArchivePartsV1) { p.config["os"] = "windows" }},
		{"wrong architecture", func(p *identityArchivePartsV1) { p.config["architecture"] = "arm64" }},
		{"wrong variant", func(p *identityArchivePartsV1) { p.config["variant"] = "v8" }},
		{"null variant", func(p *identityArchivePartsV1) { p.config["variant"] = nil }},
		{"platform field alias", func(p *identityArchivePartsV1) { p.config["OS"] = "linux" }},
		{"rootfs field alias", func(p *identityArchivePartsV1) { p.config["RootFS"] = p.config["rootfs"] }},
		{"null rootfs", func(p *identityArchivePartsV1) { p.config["rootfs"] = nil }},
		{"wrong rootfs type", func(p *identityArchivePartsV1) { p.config["rootfs"].(map[string]any)["type"] = "other" }},
		{"DiffID alias", func(p *identityArchivePartsV1) { p.config["rootfs"].(map[string]any)["Diff_Ids"] = []string{} }},
		{"empty DiffIDs", func(p *identityArchivePartsV1) { p.config["rootfs"].(map[string]any)["diff_ids"] = []string{} }},
		{"null DiffIDs", func(p *identityArchivePartsV1) { p.config["rootfs"].(map[string]any)["diff_ids"] = nil }},
		{"invalid DiffID", func(p *identityArchivePartsV1) {
			p.config["rootfs"].(map[string]any)["diff_ids"] = []string{"not-a-digest"}
		}},
		{"wrong subject", func(p *identityArchivePartsV1) {
			p.config["rootfs"].(map[string]any)["diff_ids"] = []canonical.Digest{rendererDigest("a"), rendererDigest("b")}
		}},
		{"Layers alias", func(p *identityArchivePartsV1) { p.manifest["layers"] = p.manifest["Layers"] }},
		{"Config alias", func(p *identityArchivePartsV1) { p.manifest["config"] = "other.json" }},
		{"null Layers", func(p *identityArchivePartsV1) { p.manifest["Layers"] = nil }},
		{"missing layer", func(p *identityArchivePartsV1) { p.manifest["Layers"] = []string{"missing.tar", "layer-001.tar"} }},
		{"repeated layer with mismatched DiffID", func(p *identityArchivePartsV1) { p.manifest["Layers"] = []string{"layer-000.tar", "layer-000.tar"} }},
		{"wrong layer order", func(p *identityArchivePartsV1) { p.manifest["Layers"] = []string{"layer-001.tar", "layer-000.tar"} }},
		{"wrong layer count", func(p *identityArchivePartsV1) { p.manifest["Layers"] = []string{"layer-000.tar"} }},
		{"tampered layer", func(p *identityArchivePartsV1) {
			p.members[1].content = healthArchive(t, healthArchiveMember{name: "ordinary"})
		}},
		{"selected layer is link", func(p *identityArchivePartsV1) {
			p.members[1].kind = tar.TypeSymlink
			p.members[1].linkname = "elsewhere.tar"
			p.members[1].content = nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			image, archive := identitySourceArchiveV1(t, [][]byte{base, top}, test.edit)
			requireIdentitySourceRejectedV1(t, archive, image)
		})
	}
	image, archive := identitySourceArchiveV1(t, [][]byte{base}, nil)
	t.Run("wrong locked subject", func(t *testing.T) {
		bad := image
		bad.RootFSSubject = rendererDigest("f")
		requireIdentitySourceRejectedV1(t, archive, bad)
	})
	t.Run("wrong immutable config", func(t *testing.T) {
		bad := image
		bad.ConfigDigest = rendererDigest("f")
		requireIdentitySourceRejectedV1(t, archive, bad)
	})
}

func TestImageIdentityArchiveV1UnsupportedTransformsRejectBeforeLaterRepair(t *testing.T) {
	base := healthArchive(t, identitySourceMembersV1()...)
	cases := []healthArchiveMember{
		{name: "etc", kind: tar.TypeSymlink, linkname: "elsewhere"},
		{name: ".", kind: tar.TypeReg},
		{name: "etc/passwd/child"},
		{name: "etc/group/child"},
		{name: "etc/nsswitch.conf/child"},
		{name: ".wh.etc"}, {name: ".wh..wh..opq"}, {name: "etc/.wh..wh..opq"},
		{name: "etc/.wh.passwd"}, {name: "etc/.wh.group"}, {name: "etc/.wh.nsswitch.conf"},
		{name: "other", kind: tar.TypeLink, linkname: "etc"},
		{name: "other", kind: tar.TypeLink, linkname: "./etc/passwd"},
		{name: "other", kind: tar.TypeLink, linkname: "etc/group"},
		{name: "other", kind: tar.TypeLink, linkname: "etc/nsswitch.conf"},
		{name: "other", kind: tar.TypeLink, linkname: "etc/passwd/child"},
		{name: "other", kind: tar.TypeLink, linkname: "../etc/passwd"},
	}
	for _, selected := range imageIdentityAccountPathsV1 {
		for _, kind := range []byte{tar.TypeDir, tar.TypeSymlink, tar.TypeLink, tar.TypeFifo, tar.TypeChar, tar.TypeBlock} {
			cases = append(cases, healthArchiveMember{name: selected, kind: kind, linkname: "ordinary"})
		}
	}
	for index, member := range cases {
		t.Run(fmt.Sprintf("%02d-%s-%c", index, member.name, member.kind), func(t *testing.T) {
			bad := healthArchive(t, member)
			image, archive := identitySourceArchiveV1(t, [][]byte{base, bad, base}, nil)
			requireIdentitySourceRejectedV1(t, archive, image)
		})
	}
}

func TestImageIdentityArchiveV1LiteralPathNormalization(t *testing.T) {
	base := healthArchive(t, identitySourceMembersV1()...)
	for _, name := range []string{"/etc/passwd", "../etc/passwd", "etc/../etc/passwd", "etc//passwd", "././etc/passwd", "etc\\passwd", strings.Repeat("x", 4097)} {
		t.Run(fmt.Sprintf("invalid-%q", name[:min(len(name), 50)]), func(t *testing.T) {
			bad := healthArchive(t, healthArchiveMember{name: name})
			image, archive := identitySourceArchiveV1(t, [][]byte{base, bad, base}, nil)
			requireIdentitySourceRejectedV1(t, archive, image)
		})
	}
	// Go's writer refuses a regular filename ending in '/'. Construct that
	// hostile on-wire spelling with a valid header checksum instead.
	bad := healthArchive(t, healthArchiveMember{name: "etc/passwd_"})
	copy(bad[:100], append([]byte("etc/passwd/"), make([]byte, 89)...))
	for i := 148; i < 156; i++ {
		bad[i] = ' '
	}
	sum := 0
	for _, value := range bad[:512] {
		sum += int(value)
	}
	copy(bad[148:156], fmt.Sprintf("%06o\x00 ", sum))
	image, archive := identitySourceArchiveV1(t, [][]byte{base, bad}, nil)
	requireIdentitySourceRejectedV1(t, archive, image)
	for _, pair := range [][]healthArchiveMember{
		{{name: "etc/passwd", content: []byte("a")}, {name: "./etc/passwd", content: []byte("b")}},
		{{name: "etc", kind: tar.TypeDir}, {name: "./etc/", kind: tar.TypeDir}},
		{{name: ".", kind: tar.TypeDir}, {name: "./", kind: tar.TypeDir}},
	} {
		image, archive := identitySourceArchiveV1(t, [][]byte{base, healthArchive(t, pair...)}, nil)
		requireIdentitySourceRejectedV1(t, archive, image)
	}
}

func TestImageIdentityArchiveV1FramingAndAccountValidation(t *testing.T) {
	base := healthArchive(t, identitySourceMembersV1()...)
	for _, test := range []struct {
		name  string
		layer []byte
	}{
		{"no tar", []byte("SECRET not a tar")},
		{"truncated header", base[:511]},
		{"missing terminator", base[:len(base)-1024]},
		{"partial terminator", base[:len(base)-512]},
		{"nonzero trailer", append(append([]byte{}, base...), bytes.Repeat([]byte{'x'}, 512)...)},
		{"unaligned zero trailer", append(append([]byte{}, base...), 0)},
		{"gzip layer", []byte{0x1f, 0x8b, 8, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			image, archive := identitySourceArchiveV1(t, [][]byte{test.layer}, nil)
			requireIdentitySourceRejectedV1(t, archive, image)
		})
	}
	for _, test := range []struct {
		name    string
		members []healthArchiveMember
	}{
		{"missing account file", identitySourceMembersV1()[:4]},
		{"duplicate numeric account", []healthArchiveMember{{name: "etc/passwd", content: []byte("worker:x:200:200::/:/bin/sh\nalias:x:200:200::/:/bin/sh\n")}}},
		{"dynamic NSS", []healthArchiveMember{{name: "etc/nsswitch.conf", content: []byte("passwd: files ldap\ngroup: files\n")}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			layers := [][]byte{base, healthArchive(t, test.members...)}
			if test.name == "missing account file" {
				layers = layers[1:]
			}
			image, archive := identitySourceArchiveV1(t, layers, nil)
			requireIdentitySourceRejectedV1(t, archive, image)
		})
	}
	image, archive := identitySourceArchiveV1(t, [][]byte{base}, nil)
	for _, bad := range [][]byte{archive[:len(archive)-512], append(append([]byte{}, archive...), bytes.Repeat([]byte{'x'}, 512)...)} {
		requireIdentitySourceRejectedV1(t, bad, image)
	}
}

func TestImageIdentityArchiveV1ResourceBounds(t *testing.T) {
	base := healthArchive(t, identitySourceMembersV1()...)
	t.Run("layer candidate count", func(t *testing.T) {
		layers := make([][]byte, imageIdentityLayerLimitV1+1)
		for i := range layers {
			layers[i] = healthArchive(t)
		}
		image, archive := identitySourceArchiveV1(t, layers, nil)
		requireIdentitySourceRejectedV1(t, archive, image)
		layers[imageIdentityLayerLimitV1-1] = base
		image, archive = identitySourceArchiveV1(t, layers[:imageIdentityLayerLimitV1], nil)
		policy, platform := identitySourcePolicyV1(t)
		if _, err := readDockerImageIdentityArchiveV1(t.Context(), bytes.NewReader(archive), policy, image, platform); err != nil {
			t.Fatalf("maximum layer count rejected: %v", err)
		}
	})
	t.Run("headers per layer", func(t *testing.T) {
		members := make([]healthArchiveMember, imageIdentityHeaderLimitV1+1)
		for i := range members {
			members[i].name = fmt.Sprintf("ordinary-%06d", i)
		}
		image, archive := identitySourceArchiveV1(t, [][]byte{base, healthArchive(t, members...)}, nil)
		requireIdentitySourceRejectedV1(t, archive, image)
	})
	t.Run("aggregate names per layer", func(t *testing.T) {
		members := make([]healthArchiveMember, 2100)
		for i := range members {
			members[i].name = strings.Repeat("x", 4080) + fmt.Sprintf("-%04d", i)
		}
		image, archive := identitySourceArchiveV1(t, [][]byte{base, healthArchive(t, members...)}, nil)
		requireIdentitySourceRejectedV1(t, archive, image)
	})
	for _, test := range []struct {
		name    string
		content []byte
	}{
		{"oversized file", bytes.Repeat([]byte{'x'}, imageIdentityFileLimitV1+1)},
		{"oversized line even when superseded", bytes.Repeat([]byte{'x'}, (64<<10)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := healthArchive(t, healthArchiveMember{name: "etc/passwd", content: test.content})
			image, archive := identitySourceArchiveV1(t, [][]byte{bad, base}, nil)
			requireIdentitySourceRejectedV1(t, archive, image)
		})
	}
	t.Run("aggregate selected content", func(t *testing.T) {
		line := append([]byte{'#'}, bytes.Repeat([]byte{'x'}, (64<<10)-2)...)
		line = append(line, '\n')
		large := healthArchive(t, healthArchiveMember{name: "etc/passwd", content: bytes.Repeat(line, 16)})
		layers := make([][]byte, 17)
		for i := range layers {
			layers[i] = large
		}
		image, archive := identitySourceArchiveV1(t, append(layers, base), nil)
		requireIdentitySourceRejectedV1(t, archive, image)
		image, archive = identitySourceArchiveV1(t, append(layers[:15], base), nil)
		policy, platform := identitySourcePolicyV1(t)
		if _, err := readDockerImageIdentityArchiveV1(t.Context(), bytes.NewReader(archive), policy, image, platform); err != nil {
			t.Fatalf("bounded superseded content rejected: %v", err)
		}
	})
}

func identitySourceBuilderContextV1(ctx context.Context) context.Context {
	return context.WithValue(ctx, generatedBuilderSessionKey{}, &generatedBuilderSession{builder: &generatedImageBuilder{mode: generatedPodmanAPI, endpoint: "unix:///private-health.sock"}, attempted: true})
}

func TestImageIdentityArchiveV1ExportLifecycleAndRedaction(t *testing.T) {
	policy, platform := identitySourcePolicyV1(t)
	image, archive := identitySourceArchiveV1(t, [][]byte{healthArchive(t, identitySourceMembersV1()...)}, nil)
	t.Run("selected endpoint and immutable config only", func(t *testing.T) {
		calls := installHealthDockerFixture(t, archive, false)
		binary := filepath.Join(filepath.Dir(calls), "docker")
		script, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		script = bytes.Replace(script, []byte("printf x"), []byte("[ \"$3\" = \"$IDENTITY_CONFIG\" ] && [ \"$#\" = 3 ] || exit 19\nprintf x"), 1)
		if err := os.WriteFile(binary, script, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("IDENTITY_CONFIG", string(image.ConfigDigest))
		evidence, err := exportDockerImageIdentityV1(identitySourceBuilderContextV1(t.Context()), policy, image, platform)
		if err != nil || evidence.Image != image {
			t.Fatalf("got %#v, %v", evidence, err)
		}
		if data, err := os.ReadFile(calls); err != nil || string(data) != "x" {
			t.Fatalf("unexpected export calls %q, %v", data, err)
		}
	})
	t.Run("valid stream with failed exit", func(t *testing.T) {
		installHealthDockerFixture(t, archive, true)
		evidence, err := exportDockerImageIdentityV1(identitySourceBuilderContextV1(t.Context()), policy, image, platform)
		if err == nil || err.Error() != "image identity export failed (exit 17)" || !reflect.DeepEqual(evidence, deploy.ImageIdentityEvidenceV1{}) {
			t.Fatalf("got %#v, %v", evidence, err)
		}
	})
	t.Run("parser failure", func(t *testing.T) {
		installHealthDockerFixture(t, bytes.Repeat([]byte("SECRET"), 256), false)
		evidence, err := exportDockerImageIdentityV1(identitySourceBuilderContextV1(t.Context()), policy, image, platform)
		if err == nil || err.Error() != "image identity export failed" || !reflect.DeepEqual(evidence, deploy.ImageIdentityEvidenceV1{}) {
			t.Fatalf("got %#v, %v", evidence, err)
		}
	})
	t.Run("context cancels blocked complete stream", func(t *testing.T) {
		calls := installHealthDockerFixture(t, archive, false)
		binary := filepath.Join(filepath.Dir(calls), "docker")
		script, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binary, append(script, []byte("exec sleep 30\n")...), 0700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		evidence, err := exportDockerImageIdentityV1(identitySourceBuilderContextV1(ctx), policy, image, platform)
		if err == nil || !reflect.DeepEqual(evidence, deploy.ImageIdentityEvidenceV1{}) {
			t.Fatalf("got %#v, %v", evidence, err)
		}
	})
	t.Run("no builder and invalid input cause no command", func(t *testing.T) {
		calls := installHealthDockerFixture(t, archive, false)
		if _, err := exportDockerImageIdentityV1(t.Context(), policy, image, platform); err == nil {
			t.Fatal("accepted export without selected builder")
		}
		for _, ctx := range []context.Context{t.Context(), identitySourceBuilderContextV1(t.Context())} {
			if _, err := exportDockerImageIdentityV1(ctx, nil, image, platform); err == nil {
				t.Fatal("accepted invalid source")
			}
		}
		if _, err := os.Stat(calls); !os.IsNotExist(err) {
			t.Fatalf("unexpected command effect: %v", err)
		}
	})
}

type identitySourceErrorReaderV1 struct{}

func (identitySourceErrorReaderV1) Read([]byte) (int, error) {
	return 0, fmt.Errorf("SECRET engine stream error")
}

func TestImageIdentityArchiveV1StreamFailureAndSparseSources(t *testing.T) {
	policy, platform := identitySourcePolicyV1(t)
	base := healthArchive(t, identitySourceMembersV1()...)
	image, archive := identitySourceArchiveV1(t, [][]byte{base}, nil)
	for _, input := range []io.Reader{identitySourceErrorReaderV1{}, io.MultiReader(bytes.NewReader(archive), identitySourceErrorReaderV1{})} {
		evidence, err := readDockerImageIdentityArchiveV1(t.Context(), input, policy, image, platform)
		if err == nil || strings.Contains(err.Error(), "SECRET") || !reflect.DeepEqual(evidence, deploy.ImageIdentityEvidenceV1{}) {
			t.Fatalf("stream error accepted or disclosed: %#v, %v", evidence, err)
		}
	}
	for _, layer := range [][]byte{
		healthArchive(t, healthArchiveMember{name: "ordinary", kind: tar.TypeGNUSparse, format: tar.FormatGNU}),
		bytes.ReplaceAll(healthArchive(t, healthArchiveMember{name: "ordinary", format: tar.FormatPAX, paxRecords: map[string]string{"FOO.sparse.map": "0,0", "FOO.sparse.numblocks": "1", "FOO.sparse.size": "274877906944"}}), []byte("FOO.sparse."), []byte("GNU.sparse.")),
	} {
		image, archive := identitySourceArchiveV1(t, [][]byte{base, layer, base}, nil)
		requireIdentitySourceRejectedV1(t, archive, image)
	}
	image, archive = identitySourceArchiveV1(t, [][]byte{base}, func(p *identityArchivePartsV1) {
		ids, _ := json.Marshal(p.config["rootfs"].(map[string]any)["diff_ids"])
		p.config["rootfs"] = json.RawMessage(`{"type":"layers","type":"layers","diff_ids":` + string(ids) + `}`)
	})
	requireIdentitySourceRejectedV1(t, archive, image)
}

func TestImageIdentityArchiveV1HiddenMetadataBudget(t *testing.T) {
	base := healthArchive(t, identitySourceMembersV1()...)
	pax := healthArchive(t, healthArchiveMember{name: strings.Repeat("x", 200), format: tar.FormatPAX})
	if pax[156] != tar.TypeXHeader {
		t.Fatal("fixture must begin with a PAX extension header")
	}
	// Repeated metadata headers do not appear in tar.Reader.Next results.
	// Supply the real byte stream without allocating the complete large layer.
	inputs := make([]io.Reader, imageIdentityHeaderLimitV1+2)
	for i := range inputs[:len(inputs)-1] {
		inputs[i] = bytes.NewReader(pax[:1024])
	}
	inputs[len(inputs)-1] = bytes.NewReader(base)
	layers := &imageIdentityLayersV1{}
	if err := layers.add(t.Context(), "layer.tar", io.MultiReader(inputs...)); err == nil {
		t.Fatal("accepted more than 100000 physical headers hidden behind one visible member")
	}
	layers = &imageIdentityLayersV1{}
	if err := layers.add(t.Context(), "layer.tar", io.MultiReader(bytes.NewReader(pax[:1024]), bytes.NewReader(base))); err != nil {
		t.Fatalf("ordinary PAX metadata must remain supported: %v", err)
	}
}

func TestImageIdentityArchiveV1MetadataBudgetCannotSupplyEOF(t *testing.T) {
	pax := healthArchive(t, healthArchiveMember{name: strings.Repeat("x", 200), format: tar.FormatPAX})[:512]
	copy(pax[124:136], fmt.Sprintf("%011o\x00", 0))
	for i := 148; i < 156; i++ {
		pax[i] = ' '
	}
	sum := 0
	for _, value := range pax {
		sum += int(value)
	}
	copy(pax[148:156], fmt.Sprintf("%06o\x00 ", sum))
	inputs := make([]io.Reader, imageIdentityHeaderLimitV1+3)
	for i := range inputs[:len(inputs)-1] {
		inputs[i] = bytes.NewReader(pax)
	}
	inputs[len(inputs)-1] = bytes.NewReader(make([]byte, 1024))
	if err := (&imageIdentityLayersV1{}).add(t.Context(), "layer.tar", io.MultiReader(inputs...)); err == nil {
		t.Fatal("header-budget EOF replaced a real tar terminator")
	}
}
