package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type healthArchiveMember struct {
	name       string
	content    []byte
	kind       byte
	size       int64
	format     tar.Format
	paxRecords map[string]string
}

func healthArchive(t *testing.T, members ...healthArchiveMember) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, member := range members {
		kind := member.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(member.content))
		if member.size != 0 {
			size = member.size
		}
		header := &tar.Header{Name: member.name, Mode: 0600, Size: size, Typeflag: kind, Format: member.format, PAXRecords: member.paxRecords}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(member.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func healthConfigFixture(t *testing.T, config string) (string, string, []byte) {
	t.Helper()
	id := fmtHealthDigest([]byte(config))
	name := strings.TrimPrefix(id, "sha256:") + ".json"
	manifest, _ := json.Marshal([]map[string]string{{"Config": name}})
	archive := healthArchive(t, healthArchiveMember{name: name, content: []byte(config)}, healthArchiveMember{name: "manifest.json", content: manifest})
	return id, name, archive
}
func fmtHealthDigest(data []byte) string {
	digest := sha256.Sum256(data)
	const hex = "0123456789abcdef"
	result := make([]byte, 64)
	for i, v := range digest {
		result[2*i] = hex[v>>4]
		result[2*i+1] = hex[v&15]
	}
	return "sha256:" + string(result)
}

func TestImageHealthArchivePreservesActualMetadata(t *testing.T) {
	for _, test := range []struct {
		name, config string
		want         *dockerHealthcheck
	}{
		{name: "absent", config: `{"config":{}}`},
		{name: "null", config: `{"config":{"Healthcheck":null}}`},
		{name: "disabled", config: `{"config":{"Healthcheck":{"Test":["NONE"]}}}`, want: &dockerHealthcheck{Test: []string{"NONE"}}},
		{name: "active", config: `{"config":{"Healthcheck":{"Test":["CMD","/bin/true"],"Interval":60000000000,"Timeout":5000000000,"Retries":3,"StartPeriod":1000000000,"StartInterval":2000000000}}}`, want: &dockerHealthcheck{Test: []string{"CMD", "/bin/true"}, Interval: 60000000000, Timeout: 5000000000, Retries: 3, StartPeriod: 1000000000, StartInterval: 2000000000}},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, _, archive := healthConfigFixture(t, test.config)
			actual, err := readDockerImageHealthcheckArchive(t.Context(), bytes.NewReader(archive), id)
			if err != nil || !reflect.DeepEqual(actual, test.want) {
				t.Fatalf("got %#v, %v; want %#v", actual, err, test.want)
			}
		})
	}
}

func TestImageHealthArchiveManifestConfigSelectorIsCanonical(t *testing.T) {
	config := []byte(`{"config":{"Healthcheck":{"Test":["NONE"]}}}`)
	id := fmtHealthDigest(config)
	name := strings.TrimPrefix(id, "sha256:") + ".json"
	build := func(manifest string) []byte {
		return healthArchive(t, healthArchiveMember{name: name, content: config}, healthArchiveMember{name: "manifest.json", content: []byte(manifest)})
	}
	for _, test := range []struct {
		name     string
		manifest string
		wantErr  bool
	}{
		{name: "canonical", manifest: `[{"Config":"` + name + `"}]`},
		{name: "uppercase then lowercase conflict", manifest: `[{"Config":"wrong.json","config":"` + name + `"}]`, wantErr: true},
		{name: "lowercase then uppercase conflict", manifest: `[{"config":"` + name + `","Config":"wrong.json"}]`, wantErr: true},
		{name: "lowercase only", manifest: `[{"config":"` + name + `"}]`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !json.Valid([]byte(test.manifest)) {
				t.Fatalf("manifest selector fixture is invalid JSON: %s", test.manifest)
			}
			actual, err := readDockerImageHealthcheckArchive(t.Context(), bytes.NewReader(build(test.manifest)), id)
			if test.wantErr {
				if err == nil {
					t.Fatalf("accepted noncanonical manifest selector: %#v", actual)
				}
				return
			}
			if err != nil || actual == nil || !reflect.DeepEqual(actual.Test, []string{"NONE"}) {
				t.Fatalf("canonical manifest rejected: %#v, %v", actual, err)
			}
		})
	}
}

func TestImageHealthArchiveRejectsSparseMembersPromptly(t *testing.T) {
	config := []byte(`{"config":{"Healthcheck":{"Test":["NONE"]}}}`)
	id := fmtHealthDigest(config)
	name := strings.TrimPrefix(id, "sha256:") + ".json"
	canonical := healthArchive(t, healthArchiveMember{name: name, content: config}, healthArchiveMember{name: "manifest.json", content: []byte(`[{"Config":"` + name + `"}]`)})
	ordinaryLayer := healthArchive(t, healthArchiveMember{name: "layer.tar", content: []byte("ordinary layer payload")}, healthArchiveMember{name: name, content: config}, healthArchiveMember{name: "manifest.json", content: []byte(`[{"Config":"` + name + `"}]`)})
	for _, test := range []struct {
		name    string
		archive []byte
	}{
		{name: "old GNU sparse type", archive: healthArchive(t, healthArchiveMember{name: "layer.tar", kind: tar.TypeGNUSparse, format: tar.FormatGNU}, healthArchiveMember{name: name, content: config}, healthArchiveMember{name: "manifest.json", content: []byte(`[{"Config":"` + name + `"}]`)})},
		{name: "PAX sparse attributes", archive: bytes.ReplaceAll(healthArchive(t, healthArchiveMember{name: "layer.tar", format: tar.FormatPAX, paxRecords: map[string]string{"FOO.sparse.map": "0,0", "FOO.sparse.numblocks": "1", "FOO.sparse.size": "274877906944"}}, healthArchiveMember{name: name, content: config}, healthArchiveMember{name: "manifest.json", content: []byte(`[{"Config":"` + name + `"}]`)}), []byte("FOO.sparse."), []byte("GNU.sparse."))},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
			defer cancel()
			start := time.Now()
			if _, err := readDockerImageHealthcheckArchive(ctx, bytes.NewReader(test.archive), id); err == nil {
				t.Fatal("accepted sparse archive member")
			} else if time.Since(start) > 100*time.Millisecond {
				t.Fatalf("sparse archive member was not rejected promptly: %v", err)
			}
		})
	}
	if actual, err := readDockerImageHealthcheckArchive(t.Context(), bytes.NewReader(canonical), id); err != nil || actual == nil {
		t.Fatalf("canonical archive rejected: %#v, %v", actual, err)
	}
	if actual, err := readDockerImageHealthcheckArchive(t.Context(), bytes.NewReader(ordinaryLayer), id); err != nil || actual == nil {
		t.Fatalf("ordinary layer payload rejected: %#v, %v", actual, err)
	}
}

func TestImageHealthArchiveRejectsInvalidEvidence(t *testing.T) {
	config := []byte(`{"config":{"Healthcheck":{"Test":["NONE"]}}}`)
	id, name, good := healthConfigFixture(t, string(config))
	manifest := []byte(`[{"Config":"` + name + `"}]`)
	build := func(items ...healthArchiveMember) []byte { return healthArchive(t, items...) }
	c := healthArchiveMember{name: name, content: config}
	m := healthArchiveMember{name: "manifest.json", content: manifest}
	badConfig := []byte(`{"config":{"Healthcheck":null}}`)
	badHash := build(healthArchiveMember{name: name, content: badConfig}, m)
	cases := map[string][]byte{
		"wrong digest":     badHash,
		"missing manifest": build(c), "missing config": build(m),
		"duplicate manifest": build(c, m, m), "duplicate config": build(c, c, m),
		"multiple manifest images": build(c, healthArchiveMember{name: "manifest.json", content: []byte(`[{"Config":"` + name + `"},{"Config":"` + name + `"}]`)}),
		"wrong manifest config":    build(c, healthArchiveMember{name: "manifest.json", content: []byte(`[{"Config":"other.json"}]`)}),
		"duplicate manifest key":   build(c, healthArchiveMember{name: "manifest.json", content: []byte(`[{"Config":"other.json","Config":"` + name + `"}]`)}),
		"metadata symlink":         build(healthArchiveMember{name: name, kind: tar.TypeSymlink}, m),
		"truncated payload":        good[:700], "missing terminator": good[:len(good)-1024],
		"partial final block": good[:len(good)-1], "trailing data": append(append([]byte{}, good...), []byte("other archive")...),
		"unsafe name":                build(c, m, healthArchiveMember{name: "../extra", content: []byte("x")}),
		"duplicate unrelated member": build(c, m, healthArchiveMember{name: "extra", content: []byte("a")}, healthArchiveMember{name: "extra", content: []byte("b")}),
	}
	for label, archive := range cases {
		t.Run(label, func(t *testing.T) {
			if actual, err := readDockerImageHealthcheckArchive(t.Context(), bytes.NewReader(archive), id); err == nil {
				t.Fatalf("accepted invalid evidence: %#v", actual)
			}
		})
	}
	for label, text := range map[string]string{
		"duplicate config key":  `{"config":{},"config":{"Healthcheck":{"Test":["NONE"]}}}`,
		"health alias":          `{"config":{"Healthcheck":{"Test":["CMD","true"],"test":["NONE"]}}}`,
		"config health alias":   `{"config":{"healthcheck":{"Test":["CMD","true"]}}}`,
		"duplicate health key":  `{"config":{"Healthcheck":{"Test":["CMD","true"],"Test":["NONE"]}}}`,
		"missing actual config": `{"container_config":{"Healthcheck":{"Test":["NONE"]}}}`,
		"null actual config":    `{"config":null}`, "array actual config": `{"config":[]}`,
		"malformed config": `{"config":`, "invalid health": `{"config":{"Healthcheck":"NONE"}}`,
		"multiple JSON": `{"config":{}} {"config":{}}`,
	} {
		t.Run(label, func(t *testing.T) {
			actualID, _, archive := healthConfigFixture(t, text)
			if _, err := readDockerImageHealthcheckArchive(t.Context(), bytes.NewReader(archive), actualID); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	t.Run("oversized config", func(t *testing.T) {
		large := bytes.Repeat([]byte(" "), int(imageHealthConfigLimit)+1)
		archive := build(healthArchiveMember{name: name, content: large}, m)
		if _, err := readDockerImageHealthcheckArchive(t.Context(), bytes.NewReader(archive), id); err == nil {
			t.Fatal("accepted oversized config")
		}
	})
	t.Run("oversized manifest", func(t *testing.T) {
		archive := build(c, healthArchiveMember{name: "manifest.json", content: bytes.Repeat([]byte(" "), int(imageHealthManifestLimit)+1)})
		if _, err := readDockerImageHealthcheckArchive(t.Context(), bytes.NewReader(archive), id); err == nil {
			t.Fatal("accepted oversized manifest")
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := readDockerImageHealthcheckArchive(ctx, bytes.NewReader(good), id); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("read failure", func(t *testing.T) {
		if _, err := readDockerImageHealthcheckArchive(t.Context(), io.MultiReader(bytes.NewReader(good[:700]), healthFailReader{}), id); err == nil {
			t.Fatal("accepted reader failure")
		}
	})
}

type healthFailReader struct{}

func (healthFailReader) Read([]byte) (int, error) { return 0, errors.New("fixture read failure") }

type healthReadOnlyReader struct{ io.Reader }

func TestImageHealthStderrCopyBound(t *testing.T) {
	var output boundedImageHealthStderr
	if _, err := io.Copy(&output, healthReadOnlyReader{strings.NewReader(strings.Repeat("x", 1<<20))}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != imageHealthStderrLimit {
		t.Fatalf("stderr retained %d bytes, want %d", output.Len(), imageHealthStderrLimit)
	}
}

func TestImageHealthExecStderrCopyBound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixture")
	}
	var output boundedImageHealthStderr
	cmd := exec.Command("sh", "-c", "head -c 1048576 /dev/zero >&2")
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if output.Len() != imageHealthStderrLimit {
		t.Fatalf("stderr retained %d bytes, want %d", output.Len(), imageHealthStderrLimit)
	}
}

// The fixture command verifies endpoint pinning and lets the real command/pipe
// lifecycle run, including valid metadata followed by a failing exit.
func installHealthDockerFixture(t *testing.T, archive []byte, fail bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixture")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "image.tar")
	if err := os.WriteFile(input, archive, 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
[ "$DOCKER_HOST" = "unix:///private-health.sock" ] || exit 13
[ -z "$DOCKER_CONTEXT" ] || exit 14
[ "$1 $2" = "image save" ] || exit 15
printf x >> "$HEALTH_CALLS"
cat "$HEALTH_ARCHIVE"
`
	if fail {
		script += "echo export-failed >&2\nexit 17\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HEALTH_ARCHIVE", input)
	t.Setenv("HEALTH_CALLS", filepath.Join(dir, "calls"))
	t.Setenv("DOCKER_HOST", "unix:///wrong.sock")
	t.Setenv("DOCKER_CONTEXT", "wrong")
	return filepath.Join(dir, "calls")
}
func TestImageHealthExportFailureAndCancellation(t *testing.T) {
	id, _, archive := healthConfigFixture(t, `{"config":{"Healthcheck":{"Test":["NONE"]}}}`)
	t.Run("command failure after valid archive", func(t *testing.T) {
		installHealthDockerFixture(t, archive, true)
		if _, err := exportDockerImageHealthcheck(t.Context(), "unix:///private-health.sock", id); err == nil || !strings.Contains(err.Error(), "export-failed") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("parse failure stops command", func(t *testing.T) {
		installHealthDockerFixture(t, []byte("not a tar"), false)
		if _, err := exportDockerImageHealthcheck(t.Context(), "unix:///private-health.sock", id); err == nil {
			t.Fatal("accepted failed parse")
		}
	})
	t.Run("canceled before start", func(t *testing.T) {
		installHealthDockerFixture(t, archive, false)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := exportDockerImageHealthcheck(ctx, "unix:///private-health.sock", id); err == nil {
			t.Fatal("accepted canceled export")
		}
	})
	t.Run("canceled during stream", func(t *testing.T) {
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
		start := time.Now()
		if _, err := exportDockerImageHealthcheck(ctx, "unix:///private-health.sock", id); err == nil {
			t.Fatal("accepted canceled stream")
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("export process did not stop promptly")
		}
	})

}
func TestImageHealthRestoreCachesVerifiedConfigAndKeepsPolicy(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, raw string
		disabled  bool
	}{
		{"disabled", `{"config":{"Healthcheck":{"Test":["NONE"]}}}`, true},
		{"absent", `{"config":{}}`, false},
		{"active", `{"config":{"Healthcheck":{"Test":["CMD","true"],"Interval":60000000000}}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, _, archive := healthConfigFixture(t, test.raw)
			calls := installHealthDockerFixture(t, archive, false)
			builder := &generatedImageBuilder{mode: generatedPodmanAPI, endpoint: "unix:///private-health.sock"}
			ctx, err := bindMaterializationBuilderContext(t.Context(), builder)
			if err != nil {
				t.Fatal(err)
			}
			record := dockerImageInspectRecord{ID: id, OS: "linux", Architecture: "amd64", Config: &dockerImageConfig{User: "1000:1000", WorkingDir: "/work", StopSignal: "SIGTERM"}}
			record.RootFS.Layers = []string{"sha256:" + strings.Repeat("b", 64)}
			output, _ := json.Marshal([]dockerImageInspectRecord{record})
			for i := 0; i < 2; i++ {
				restored, err := restoreDockerInspectionHealthchecks(ctx, builder.endpoint, []string{"image", "inspect", id}, string(output))
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := parseDockerImageInspectionDetails(id, platform, []byte(restored))
				if err != nil {
					t.Fatal(err)
				}
				none, _ := canonicalizeDockerHealthcheck(dockerHealthcheck{Test: []string{"NONE"}})
				image, inspectErr := inspectBuiltImageCandidate(ctx, BuiltImageCandidate{ImageID: canonical.Digest(id), builder: builder}, platform, func(context.Context, ...string) (string, error) { return restored, nil })
				if inspectErr != nil {
					t.Fatal(inspectErr)
				}
				_, request := materializationLayerFixture(t)
				policyErr := ValidateInspectedMaterializationCandidate(request.Transaction, image)
				if test.disabled && policyErr != nil {
					t.Fatalf("actual NONE rejected: %v", policyErr)
				}
				if !test.disabled && (policyErr == nil || !strings.Contains(policyErr.Error(), "healthcheck is not disabled")) {
					t.Fatalf("missing or active healthcheck accepted: %v", policyErr)
				}
				if (parsed.Config.Healthcheck == none) != test.disabled {
					t.Fatalf("metadata changed meaning: %s", parsed.Config.Healthcheck)
				}
			}
			invocations, err := os.ReadFile(calls)
			if err != nil || string(invocations) != "x" {
				t.Fatalf("export not cached: %q, %v", invocations, err)
			}
		})
	}
}
func TestImageHealthRestoreLeavesDockerInspectionUnchanged(t *testing.T) {
	for _, mode := range []generatedBuilderMode{generatedBuildKit, generatedPodmanAPI} {
		ctx, err := bindMaterializationBuilderContext(t.Context(), &generatedImageBuilder{mode: mode, endpoint: "unix:///private-health.sock"})
		if err != nil {
			t.Fatal(err)
		}
		// Docker null passes unchanged. A populated projection needs no fallback.
		output := `[{"Id":"sha256:` + strings.Repeat("a", 64) + `","Config":{"Healthcheck":null}}]`
		if mode == generatedPodmanAPI {
			output = `[{"Id":"sha256:` + strings.Repeat("a", 64) + `","Config":{"Healthcheck":{"Test":["CMD","true"]}}}]`
		}
		actual, err := restoreDockerInspectionHealthchecks(ctx, "unix:///private-health.sock", []string{"image", "inspect", "fixture"}, output)
		if err != nil || actual != output {
			t.Fatalf("inspection changed: %s %v", actual, err)
		}
	}
}
