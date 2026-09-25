package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func TestPortableRuntimePayloadImageAcceptsExactFinalInventory(t *testing.T) {
	content := []byte("browser executable")
	notice := []byte("notice")
	expected := []portableRuntimeInventoryEntryV1{
		{path: "/opt/reploy/tools/playwright", kind: providerstore.ArchiveEntryKindDirectory, uid: 0, gid: 0, mode: 0o555},
		{path: "/opt/reploy/tools/playwright/bin", kind: providerstore.ArchiveEntryKindDirectory, uid: 0, gid: 0, mode: 0o555},
		portableRuntimeTestInventoryFile("/opt/reploy/tools/playwright/bin/browser", content, 0o555),
		portableRuntimeTestInventoryFile("/opt/reploy/tools/playwright/NOTICE", notice, 0o444),
	}
	session := portableRuntimeInventoryTestSession(t, portableRuntimeTestInventoryTar(t,
		portableRuntimeTestInventoryTarEntry{path: "/playwright", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555, uid: 0, gid: 0},
		portableRuntimeTestInventoryTarEntry{path: "/playwright/bin", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555, uid: 0, gid: 0},
		portableRuntimeTestInventoryTarEntry{path: "/playwright/bin/browser", kind: providerstore.ArchiveEntryKindRegular, mode: 0o555, uid: 0, gid: 0, content: content},
		portableRuntimeTestInventoryTarEntry{path: "/playwright/NOTICE", kind: providerstore.ArchiveEntryKindRegular, mode: 0o444, uid: 0, gid: 0, content: notice},
	))
	if err := exportAndVerifyPortableRuntimeInventoryV1(context.Background(), session, expected); err != nil {
		t.Fatal(err)
	}
}

func TestPortableRuntimePayloadImageLimitsIncludeSelectedRootAndTarFraming(t *testing.T) {
	const root = "/opt/reploy/tools/playwright/browser"
	want := map[string]portableRuntimeInventoryEntryV1{
		root:         {path: root, kind: providerstore.ArchiveEntryKindDirectory},
		"/unrelated": {path: "/unrelated", kind: providerstore.ArchiveEntryKindDirectory},
	}
	for index := range providerstore.CoreMaxArchiveEntries {
		imagePath := fmt.Sprintf("%s/file-%d", root, index)
		size := int64(0)
		if index == 0 {
			size = providerstore.CoreMaxArchiveUnpackedBytes
		}
		want[imagePath] = portableRuntimeInventoryEntryV1{path: imagePath, kind: providerstore.ArchiveEntryKindRegular, size: size}
	}
	limit, err := portableRuntimeImageInventoryLimitForRootV1(want, root)
	if err != nil {
		t.Fatal(err)
	}
	if limit.entries != providerstore.CoreMaxArchiveEntries+1 {
		t.Fatalf("entry limit = %d, want archive members plus selected root", limit.entries)
	}
	wantBytes := int64(providerstore.CoreMaxArchiveUnpackedBytes) + int64(limit.entries)*portableRuntimeImageTarFramingBytesPerEntryV1 + 1024
	if limit.bytes != wantBytes {
		t.Fatalf("tar stream byte limit = %d, want %d", limit.bytes, wantBytes)
	}
}

func TestPortableRuntimePayloadImageRejectsExtraFinalInventoryEntry(t *testing.T) {
	content := []byte("browser executable")
	expected := []portableRuntimeInventoryEntryV1{
		{path: "/opt/reploy/tools/playwright", kind: providerstore.ArchiveEntryKindDirectory, uid: 0, gid: 0, mode: 0o555},
		portableRuntimeTestInventoryFile("/opt/reploy/tools/playwright/bin/browser", content, 0o555),
	}
	session := portableRuntimeInventoryTestSession(t, portableRuntimeTestInventoryTar(t,
		portableRuntimeTestInventoryTarEntry{path: "/playwright", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555, uid: 0, gid: 0},
		portableRuntimeTestInventoryTarEntry{path: "/playwright/bin/browser", kind: providerstore.ArchiveEntryKindRegular, mode: 0o555, uid: 0, gid: 0, content: content},
		portableRuntimeTestInventoryTarEntry{path: "/playwright/extra", kind: providerstore.ArchiveEntryKindRegular, mode: 0o444, uid: 0, gid: 0, content: []byte("unexpected")},
	))
	if err := exportAndVerifyPortableRuntimeInventoryV1(context.Background(), session, expected); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("extra final-image entry passed inventory verification: %v", err)
	}
}

func TestPortableRuntimePayloadImageRejectsChangedDirectoryMetadata(t *testing.T) {
	expected := []portableRuntimeInventoryEntryV1{{path: "/opt/reploy/tools/playwright", kind: providerstore.ArchiveEntryKindDirectory, uid: 0, gid: 0, mode: 0o555}}
	session := portableRuntimeInventoryTestSession(t, portableRuntimeTestInventoryTar(t,
		portableRuntimeTestInventoryTarEntry{path: "/playwright", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o755, uid: 0, gid: 0},
	))
	if err := exportAndVerifyPortableRuntimeInventoryV1(context.Background(), session, expected); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("changed final-image directory metadata passed inventory verification: %v", err)
	}
}

func TestPortableRuntimePayloadImageRejectsSpecialStagingModes(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "setuid", mode: os.ModeSetuid},
		{name: "setgid", mode: os.ModeSetgid},
		{name: "sticky", mode: os.ModeSticky},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPortableToolPythonLockedTestFixture(t)
			stubPortableRuntimeArchives(t, 0)
			payloads, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
			if err != nil {
				t.Fatal(err)
			}
			defer payloads.Cleanup()
			file := portableRuntimeTestStagedRegularPath(t, payloads)
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(file, info.Mode().Perm()|test.mode); err != nil {
				t.Fatal(err)
			}
			mutated, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if mutated.Mode()&test.mode == 0 {
				// Some host filesystems clear setuid/setgid for an unprivileged
				// test process. Still exercise the exact staging-mode guard for
				// those bits when the filesystem cannot retain the mutation.
				if _, err := normalizePortableRuntimeModeV1(info.Mode().Perm() | test.mode); err == nil {
					t.Fatalf("special staging mode guard accepted %#o", test.mode)
				}
				return
			}

			previousCollision, previousBuild := requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1
			t.Cleanup(func() {
				requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1 = previousCollision, previousBuild
			})
			collisionChecks, builds := 0, 0
			requirePortableRuntimeDestinationsAbsentV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) error {
				collisionChecks++
				return nil
			}
			buildPortableRuntimePayloadLayerV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, string, []byte, RunOptions) (BuiltImageCandidate, error) {
				builds++
				return BuiltImageCandidate{}, errors.New("unexpected build after special staging mode")
			}

			if _, err := PreparePortableRuntimePayloadLayerV1(context.Background(), fixture.store, payloads, testProbeImageDescriptor(t, "linux/amd64"), RunOptions{}); err == nil ||
				!strings.Contains(err.Error(), "special bits") {
				t.Fatalf("special staging mode was accepted: %v", err)
			}
			if collisionChecks != 0 || builds != 0 {
				t.Fatalf("special staging mode reached collision/build: collision checks=%d builds=%d", collisionChecks, builds)
			}
		})
	}
}

func TestPortableRuntimePayloadImageRejectsSpecialFinalImageModes(t *testing.T) {
	tests := []struct {
		name string
		mode int64
	}{
		{name: "setuid", mode: 0o4000},
		{name: "setgid", mode: 0o2000},
		{name: "sticky", mode: 0o1000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := []byte("browser executable")
			expected := []portableRuntimeInventoryEntryV1{
				{path: "/opt/reploy/tools/playwright", kind: providerstore.ArchiveEntryKindDirectory, uid: 0, gid: 0, mode: 0o555},
				portableRuntimeTestInventoryFile("/opt/reploy/tools/playwright/browser", content, 0o555),
			}
			session := portableRuntimeInventoryTestSession(t, portableRuntimeTestInventoryTar(t,
				portableRuntimeTestInventoryTarEntry{path: "/playwright", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555, uid: 0, gid: 0},
				portableRuntimeTestInventoryTarEntry{path: "/playwright/browser", kind: providerstore.ArchiveEntryKindRegular, mode: 0o555 | test.mode, uid: 0, gid: 0, content: content},
			))
			if err := exportAndVerifyPortableRuntimeInventoryV1(context.Background(), session, expected); err == nil || !strings.Contains(err.Error(), "special bits") {
				t.Fatalf("special final-image mode was accepted: %v", err)
			}
		})
	}
}

func TestPortableRuntimePayloadModesNormalizeWindowsRepresentation(t *testing.T) {
	payloads := &PortableRuntimePayloadsV1{executablePaths: []string{"/opt/reploy/tools/bin/browser"}}
	observed := []portableRuntimeInventoryEntryV1{
		{path: "/opt/reploy/tools", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555},
		{path: "/opt/reploy/tools/bin", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555},
		{path: "/opt/reploy/tools/bin/browser", kind: providerstore.ArchiveEntryKindRegular, mode: 0o444, size: 7},
		{path: "/opt/reploy/tools/bin/NOTICE", kind: providerstore.ArchiveEntryKindRegular, mode: 0o444, size: 6},
	}
	normalized, err := normalizePortableRuntimeExpectedInventoryV1(payloads, observed)
	if err != nil {
		t.Fatal(err)
	}
	want := []os.FileMode{0o555, 0o555, 0o555, 0o444}
	if len(normalized) != len(want) {
		t.Fatalf("normalized inventory length = %d, want %d", len(normalized), len(want))
	}
	for index, mode := range want {
		if normalized[index].mode != mode {
			t.Fatalf("normalized mode %d = %#o, want %#o", index, normalized[index].mode, mode)
		}
	}
	observedByPath := make(map[string]portableRuntimeInventoryEntryV1, len(observed))
	for _, item := range observed {
		observedByPath[item.path] = item
	}
	if err := comparePortableRuntimeHostModesV1(observedByPath, normalized); err != nil {
		t.Fatalf("Windows host mode representation was rejected: %v", err)
	}
}

func TestPortableRuntimePayloadImageAcceptsRemappedTarIDsWithContainerOwnership(t *testing.T) {
	content := []byte("browser executable")
	expected := []portableRuntimeInventoryEntryV1{
		{path: "/opt/reploy/tools/playwright", kind: providerstore.ArchiveEntryKindDirectory, uid: 0, gid: 0, mode: 0o555},
		portableRuntimeTestInventoryFile("/opt/reploy/tools/playwright/browser", content, 0o555),
	}
	archive := portableRuntimeTestInventoryTar(t,
		portableRuntimeTestInventoryTarEntry{path: "/playwright", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555, uid: 100000, gid: 100000},
		portableRuntimeTestInventoryTarEntry{path: "/playwright/browser", kind: providerstore.ArchiveEntryKindRegular, mode: 0o555, uid: 100000, gid: 100000, content: content},
	)
	session := portableRuntimeInventoryOwnershipTestSession(t, archive, portableRuntimeTestProbeResponse(t, expected))
	observed, err := collectPortableRuntimeImageInventoryV1(context.Background(), session, expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := comparePortableRuntimeInventoryV1(observed, expected); err != nil {
		t.Fatal(err)
	}
	if err := verifyPortableRuntimeInventoryOwnershipWithObservedV1(context.Background(), session, expected, observed); err != nil {
		t.Fatal(err)
	}
}

func TestPortableRuntimePayloadImageRejectsRemappedTarIDOnEmptyDirectory(t *testing.T) {
	content := []byte("browser executable")
	expected := []portableRuntimeInventoryEntryV1{
		{path: "/opt/reploy/tools/playwright", kind: providerstore.ArchiveEntryKindDirectory, uid: 0, gid: 0, mode: 0o555},
		{path: "/opt/reploy/tools/playwright/empty", kind: providerstore.ArchiveEntryKindDirectory, uid: 0, gid: 0, mode: 0o555},
		portableRuntimeTestInventoryFile("/opt/reploy/tools/playwright/browser", content, 0o555),
	}
	archive := portableRuntimeTestInventoryTar(t,
		portableRuntimeTestInventoryTarEntry{path: "/playwright", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555, uid: 100000, gid: 100000},
		portableRuntimeTestInventoryTarEntry{path: "/playwright/empty", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555, uid: 100001, gid: 100000},
		portableRuntimeTestInventoryTarEntry{path: "/playwright/browser", kind: providerstore.ArchiveEntryKindRegular, mode: 0o555, uid: 100000, gid: 100000, content: content},
	)
	session := portableRuntimeInventoryOwnershipTestSession(t, archive, portableRuntimeTestProbeResponse(t, expected))
	observed, err := collectPortableRuntimeImageInventoryV1(context.Background(), session, expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPortableRuntimeInventoryOwnershipWithObservedV1(context.Background(), session, expected, observed); err == nil || !strings.Contains(err.Error(), "differs from mapped") {
		t.Fatalf("empty-directory remapped tar ID passed ownership verification: %v", err)
	}
}

type portableRuntimeTestInventoryTarEntry struct {
	path    string
	kind    string
	mode    int64
	uid     int
	gid     int
	content []byte
}

func portableRuntimeTestInventoryFile(path string, content []byte, mode os.FileMode) portableRuntimeInventoryEntryV1 {
	hash := sha256.Sum256(content)
	return portableRuntimeInventoryEntryV1{
		path: path, kind: providerstore.ArchiveEntryKindRegular, uid: 0, gid: 0,
		mode: mode, size: int64(len(content)), digest: canonical.Digest("sha256:" + fmt.Sprintf("%x", hash[:])),
	}
}

func portableRuntimeTestInventoryTar(t *testing.T, entries ...portableRuntimeTestInventoryTarEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, entry := range entries {
		typeflag := byte(tar.TypeReg)
		if entry.kind == providerstore.ArchiveEntryKindDirectory {
			typeflag = tar.TypeDir
		}
		header := &tar.Header{Name: strings.TrimPrefix(entry.path, "/"), Typeflag: typeflag, Mode: entry.mode, Uid: entry.uid, Gid: entry.gid, Size: int64(len(entry.content))}
		if typeflag == tar.TypeDir {
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func portableRuntimeInventoryTestSession(t *testing.T, archive []byte) *ImageValidationSession {
	t.Helper()
	return &ImageValidationSession{containerName: "portable-runtime-inventory", runDocker: func(spec CommandSpec, options RunOptions) error {
		if spec.Name != "docker" || len(spec.Args) != 3 || spec.Args[0] != "cp" || spec.Args[1] != "portable-runtime-inventory:/opt/reploy/tools/playwright" || spec.Args[2] != "-" {
			t.Fatalf("unexpected inventory command: %#v", spec)
		}
		_, err := options.Stdout.Write(archive)
		return err
	}}
}

func portableRuntimeInventoryOwnershipTestSession(t *testing.T, archive, response []byte) *ImageValidationSession {
	t.Helper()
	return &ImageValidationSession{
		containerName: "portable-runtime-inventory",
		workspace:     PreparedProbeWorkspace{ContainerExecutable: "/probe"},
		runDocker: func(spec CommandSpec, options RunOptions) error {
			switch {
			case spec.Name == "docker" && len(spec.Args) == 3 && spec.Args[0] == "cp":
				_, err := options.Stdout.Write(archive)
				return err
			case spec.Name == "docker" && len(spec.Args) == 8 && spec.Args[0] == "exec" && spec.Args[7] == "/probe":
				_, err := options.Stdout.Write(response)
				return err
			default:
				t.Fatalf("unexpected ownership command: %#v", spec)
				return nil
			}
		},
	}
}

func portableRuntimeTestProbeResponse(t *testing.T, expected []portableRuntimeInventoryEntryV1) []byte {
	t.Helper()
	regulars := make([]portableRuntimeInventoryEntryV1, 0, len(expected))
	directories := map[string]portableRuntimeInventoryEntryV1{}
	for _, item := range expected {
		if item.kind == providerstore.ArchiveEntryKindRegular {
			regulars = append(regulars, item)
		} else {
			directories[item.path] = item
		}
	}
	sort.Slice(regulars, func(i, j int) bool { return regulars[i].path < regulars[j].path })
	observations := make([]probe.ExecutableObservationV1, 0, len(regulars))
	for index, item := range regulars {
		accessPaths := map[string]probe.AccessObservationV1{
			"/": {Path: "/", Kind: providerstore.ArchiveEntryKindDirectory, Mode: "0755", UID: "0", GID: "0"},
		}
		for parent := filepath.ToSlash(filepath.Dir(item.path)); parent != "/"; parent = path.Dir(parent) {
			mode := os.FileMode(0o755)
			if directory, found := directories[parent]; found {
				mode = directory.mode
			}
			accessPaths[parent] = probe.AccessObservationV1{Path: parent, Kind: providerstore.ArchiveEntryKindDirectory, Mode: fmt.Sprintf("%04o", mode), UID: "0", GID: "0"}
		}
		accessPaths[item.path] = probe.AccessObservationV1{Path: item.path, Kind: providerstore.ArchiveEntryKindRegular, Mode: fmt.Sprintf("%04o", item.mode), UID: "0", GID: "0"}
		paths := make([]string, 0, len(accessPaths))
		for path := range accessPaths {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		access := make([]probe.AccessObservationV1, 0, len(paths))
		for _, path := range paths {
			access = append(access, accessPaths[path])
		}
		observations = append(observations, probe.ExecutableObservationV1{
			ID: fmt.Sprintf("runtime_%06d", index), InvocationPath: item.path,
			Links:    []probe.LinkObservationV1{},
			Terminal: probe.FileObservationV1{Path: item.path, Kind: providerstore.ArchiveEntryKindRegular, Mode: fmt.Sprintf("%04o", item.mode), Size: fmt.Sprint(item.size), SHA256: item.digest, UID: "0", GID: "0"},
			Access:   access,
		})
	}
	content, err := canonical.Marshal(probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: observations})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestPortableRuntimePayloadImageChecksCollisionBeforeBuild(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	stubPortableRuntimeArchives(t, 0)
	payloads, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer payloads.Cleanup()
	executables, err := collectPortableRuntimeFileEvidenceV1(payloads)
	if err != nil || len(executables) != 3 {
		t.Fatalf("staged executable evidence = %#v, %v", executables, err)
	}
	previousCollision, previousBuild := requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1
	t.Cleanup(func() {
		requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1 = previousCollision, previousBuild
	})
	builds := 0
	requirePortableRuntimeDestinationsAbsentV1 = func(_ context.Context, _ providerstore.Store, _ deploy.ImageDescriptor, paths []string) error {
		if len(paths) != 3 {
			t.Fatalf("collision preflight paths = %#v", paths)
		}
		return errors.New("destination exists")
	}
	buildPortableRuntimePayloadLayerV1 = func(_ context.Context, _ providerstore.Store, _ deploy.ImageDescriptor, _ string, _ []byte, _ RunOptions) (BuiltImageCandidate, error) {
		builds++
		return BuiltImageCandidate{}, nil
	}
	upstream := testProbeImageDescriptor(t, "linux/amd64")
	if _, err := PreparePortableRuntimePayloadLayerV1(context.Background(), fixture.store, payloads, upstream, RunOptions{}); err == nil ||
		!strings.Contains(err.Error(), "destination collision") {
		t.Fatalf("collision must fail before layer publication: %v", err)
	}
	if builds != 0 {
		t.Fatalf("built %d layers after collision", builds)
	}
}

func TestPortableRuntimePayloadImageRejectsSymlinkAncestorBeforeBuild(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	stubPortableRuntimeArchives(t, 0)
	payloads, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer payloads.Cleanup()
	upstream := testProbeImageDescriptor(t, "linux/amd64")
	previousPrepare, previousOpen, previousBuild := preparePortableRuntimeProbeWorkspaceV1, openPortableRuntimeProbeSessionV1, buildPortableRuntimePayloadLayerV1
	t.Cleanup(func() {
		preparePortableRuntimeProbeWorkspaceV1, openPortableRuntimeProbeSessionV1, buildPortableRuntimePayloadLayerV1 = previousPrepare, previousOpen, previousBuild
	})
	exactChecks, ancestorChecks, destinations := 0, 0, []string{}
	preparePortableRuntimeProbeWorkspaceV1 = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return PreparedProbeWorkspace{}, func() error { return nil }, nil
	}
	openPortableRuntimeProbeSessionV1 = func(_ context.Context, descriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace) (*ImageValidationSession, error) {
		return &ImageValidationSession{descriptor: descriptor, containerName: "portable-runtime-collision", runDocker: func(spec CommandSpec, _ RunOptions) error {
			if spec.Name != "docker" || len(spec.Args) == 0 {
				t.Fatalf("unexpected collision command: %#v", spec)
			}
			switch spec.Args[0] {
			case "exec":
				if len(spec.Args) < 11 || spec.Args[8] != `for candidate in "$@"; do test ! -e "$candidate" && test ! -L "$candidate" || exit 1; done` && spec.Args[8] != portableRuntimeDestinationAncestorSymlinkCheckV1 {
					t.Fatalf("unexpected collision exec command: %#v", spec)
				}
				destinations = append([]string{}, spec.Args[10:]...)
				if spec.Args[8] == portableRuntimeDestinationAncestorSymlinkCheckV1 {
					ancestorChecks++
					return errors.New("simulated browser-root symlink")
				}
				exactChecks++
				return nil
			case "rm":
				return nil
			default:
				t.Fatalf("unexpected collision command: %#v", spec)
				return nil
			}
		}}, nil
	}
	builds := 0
	buildPortableRuntimePayloadLayerV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, string, []byte, RunOptions) (BuiltImageCandidate, error) {
		builds++
		return BuiltImageCandidate{}, errors.New("unexpected build after symlink ancestor")
	}
	if _, err := PreparePortableRuntimePayloadLayerV1(context.Background(), fixture.store, payloads, upstream, RunOptions{}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink ancestor was accepted: %v", err)
	}
	if exactChecks != 1 || ancestorChecks != 1 || builds != 0 {
		t.Fatalf("preflight checks = exact %d, ancestor %d, builds %d", exactChecks, ancestorChecks, builds)
	}
	if len(destinations) == 0 || !strings.HasPrefix(destinations[0], "/opt/reploy/tools/playwright/ms-playwright/") {
		t.Fatalf("collision destinations = %#v, want child under browser root", destinations)
	}
}

func TestPortableRuntimePayloadImageAcceptsAbsentDestinationsUnderRealAncestors(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	stubPortableRuntimeArchives(t, 0)
	payloads, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer payloads.Cleanup()
	upstream := testProbeImageDescriptor(t, "linux/amd64")
	previousPrepare, previousOpen := preparePortableRuntimeProbeWorkspaceV1, openPortableRuntimeProbeSessionV1
	t.Cleanup(func() {
		preparePortableRuntimeProbeWorkspaceV1, openPortableRuntimeProbeSessionV1 = previousPrepare, previousOpen
	})
	exactChecks, ancestorChecks := 0, 0
	validDestination := "/opt/reploy/tools/playwright/ms-playwright/browser space,=v"
	preparePortableRuntimeProbeWorkspaceV1 = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return PreparedProbeWorkspace{}, func() error { return nil }, nil
	}
	openPortableRuntimeProbeSessionV1 = func(_ context.Context, descriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace) (*ImageValidationSession, error) {
		return &ImageValidationSession{descriptor: descriptor, containerName: "portable-runtime-collision", runDocker: func(spec CommandSpec, _ RunOptions) error {
			if spec.Name != "docker" || len(spec.Args) == 0 {
				t.Fatalf("unexpected collision command: %#v", spec)
			}
			switch spec.Args[0] {
			case "exec":
				if got := spec.Args[len(spec.Args)-1]; got != validDestination {
					t.Fatalf("catalog-valid destination was not passed as one positional argument: %q", got)
				}
				if spec.Args[8] == portableRuntimeDestinationAncestorSymlinkCheckV1 {
					ancestorChecks++
				} else {
					exactChecks++
				}
				return nil
			case "rm":
				return nil
			default:
				t.Fatalf("unexpected collision command: %#v", spec)
				return nil
			}
		}}, nil
	}
	destinations := make([]string, len(payloads.copies))
	for index, copy := range payloads.copies {
		destinations[index] = copy.Destination
	}
	destinations = append(destinations, validDestination)
	if err := requirePortableRuntimeDestinationsAbsent(context.Background(), fixture.store, upstream, destinations); err != nil {
		t.Fatal(err)
	}
	if exactChecks != 1 || ancestorChecks != 1 {
		t.Fatalf("preflight checks = exact %d, ancestor %d", exactChecks, ancestorChecks)
	}
	for _, invalid := range []string{"relative", "/", "/opt/../other", "/opt/bad\nname", "/opt/bad\\name"} {
		if err := validatePortableRuntimeDestinationPathV1(invalid); err == nil {
			t.Fatalf("invalid runtime destination %q was accepted", invalid)
		}
	}
}

func TestPortableRuntimePayloadImageRejectsStagingMutationAfterMaterialization(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, file string)
	}{
		{
			name: "same-size content replacement",
			mutate: func(t *testing.T, file string) {
				content, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if len(content) == 0 {
					t.Fatal("selected staged file is empty")
				}
				content[0] ^= 0xff
				if err := os.WriteFile(file, content, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "path addition",
			mutate: func(t *testing.T, file string) {
				added := filepath.Join(filepath.Dir(file), "added-after-handoff")
				if err := os.WriteFile(added, []byte("added"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "path deletion",
			mutate: func(t *testing.T, file string) {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mode change",
			mutate: func(t *testing.T, file string) {
				if err := os.Chmod(file, 0o444); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPortableToolPythonLockedTestFixture(t)
			stubPortableRuntimeArchives(t, 0)
			payloads, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
			if err != nil {
				t.Fatal(err)
			}
			defer payloads.Cleanup()
			file := portableRuntimeTestStagedRegularPath(t, payloads)
			test.mutate(t, file)

			previousCollision, previousBuild := requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1
			t.Cleanup(func() {
				requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1 = previousCollision, previousBuild
			})
			collisionChecks, builds := 0, 0
			requirePortableRuntimeDestinationsAbsentV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) error {
				collisionChecks++
				return nil
			}
			buildPortableRuntimePayloadLayerV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, string, []byte, RunOptions) (BuiltImageCandidate, error) {
				builds++
				return BuiltImageCandidate{}, errors.New("unexpected build after staging mutation")
			}

			if _, err := PreparePortableRuntimePayloadLayerV1(context.Background(), fixture.store, payloads, testProbeImageDescriptor(t, "linux/amd64"), RunOptions{}); err == nil ||
				!strings.Contains(err.Error(), "sealed inventory") {
				t.Fatalf("staging mutation was accepted: %v", err)
			}
			if collisionChecks != 0 || builds != 0 {
				t.Fatalf("staging mutation reached collision/build: collision checks=%d builds=%d", collisionChecks, builds)
			}
		})
	}
}

func portableRuntimeTestStagedRegularPath(t *testing.T, payloads *PortableRuntimePayloadsV1) string {
	t.Helper()
	for _, item := range payloads.sealedInventory {
		if item.kind != providerstore.ArchiveEntryKindRegular {
			continue
		}
		for _, copy := range payloads.copies {
			prefix := copy.Destination + "/"
			if !strings.HasPrefix(item.path, prefix) {
				continue
			}
			relative := strings.TrimPrefix(item.path, prefix)
			return filepath.Join(payloads.contextDir, filepath.FromSlash(copy.Source), filepath.FromSlash(relative))
		}
	}
	t.Fatal("sealed runtime inventory has no regular file")
	return ""
}

func TestPortableRuntimePayloadImageRejectsWrongPlatformBeforeProbeOrBuild(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	stubPortableRuntimeArchives(t, 0)
	payloads, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer payloads.Cleanup()
	previousCollision, previousBuild := requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1
	t.Cleanup(func() {
		requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1 = previousCollision, previousBuild
	})
	requirePortableRuntimeDestinationsAbsentV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) error {
		t.Fatal("probed destinations for wrong-platform payload")
		return nil
	}
	buildPortableRuntimePayloadLayerV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, string, []byte, RunOptions) (BuiltImageCandidate, error) {
		t.Fatal("built wrong-platform payload layer")
		return BuiltImageCandidate{}, nil
	}
	upstream := testProbeImageDescriptor(t, "linux/arm64")
	if _, err := PreparePortableRuntimePayloadLayerV1(context.Background(), fixture.store, payloads, upstream, RunOptions{}); err == nil ||
		!strings.Contains(err.Error(), "platform differs") {
		t.Fatalf("wrong-platform payload must fail before image access: %v", err)
	}
}

func TestPortableRuntimePayloadImageRejectsPublicLockPlatformMutationBeforeCollisionOrBuild(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	stubPortableRuntimeArchives(t, 0)
	payloads, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer payloads.Cleanup()
	payloads.Lock.Plan.PortableToolPlan.Tools[0].Responsibilities.Payloads[0].Record.Value["platform"] = "linux/arm64"
	previousCollision, previousBuild := requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1
	t.Cleanup(func() {
		requirePortableRuntimeDestinationsAbsentV1, buildPortableRuntimePayloadLayerV1 = previousCollision, previousBuild
	})
	requirePortableRuntimeDestinationsAbsentV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) error {
		t.Fatal("probed destinations after public lock mutation")
		return nil
	}
	buildPortableRuntimePayloadLayerV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, string, []byte, RunOptions) (BuiltImageCandidate, error) {
		t.Fatal("built payload after public lock mutation")
		return BuiltImageCandidate{}, nil
	}
	if _, err := PreparePortableRuntimePayloadLayerV1(context.Background(), fixture.store, payloads, testProbeImageDescriptor(t, "linux/arm64"), RunOptions{}); err == nil ||
		!strings.Contains(err.Error(), "public lock differs") {
		t.Fatalf("public lock platform mutation was accepted: %v", err)
	}
}

func TestPortableRuntimePayloadsRejectPublicEnvironmentNewlineMutation(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	stubPortableRuntimeArchives(t, 0)
	payloads, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer payloads.Cleanup()
	payloads.Environment[0].Name += "\nRUN injected"
	payloads.Lock.Plan.PortableToolPlan.Tools[0].Runtime.Environment[0].Name += "\nRUN injected"
	dockerfile, err := payloads.Dockerfile()
	if err == nil || !strings.Contains(err.Error(), "public") {
		t.Fatalf("public environment newline mutation was accepted: dockerfile=%q err=%v", dockerfile, err)
	}
	if bytes.Contains(dockerfile, []byte("RUN injected")) {
		t.Fatalf("injected Dockerfile instruction was emitted: %q", dockerfile)
	}
}

func TestPortableRuntimePayloadImageRejectsSubstitutedEnvironment(t *testing.T) {
	selected := []providers.PortableToolEnvironmentVariableV1{{Name: "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD", Value: "1"}}
	image := InspectedImageCandidate{Config: deploy.BaseConfig{Environment: []deploy.ConfigEnvironmentVariable{{Name: "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD", Value: "0"}}}}
	if err := requirePortableRuntimeImageEnvironmentV1(image, selected); err == nil {
		t.Fatal("substituted final-image environment passed")
	}
	image.Config.Environment[0].Value = "1"
	if err := requirePortableRuntimeImageEnvironmentV1(image, selected); err != nil {
		t.Fatal(err)
	}
}

func playwrightRuntimePayloadDAGForTest(t *testing.T) (PortableToolPythonFreshPlanV1, providers.PortableToolProviderDAGV1) {
	t.Helper()
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	providerPlan := preparedPythonResolveRequest(t, testProbeImageDescriptor(t, "linux/amd64")).Plan
	domain := providers.PortableToolDomainAuthorityV1{ID: "application", Owner: providerPlan.Nodes[1].ID}
	dag, err := providers.BuildPortableToolProviderDAGV1(providerPlan, fresh.Plan, []providers.PortableToolProviderDomainSetV1{{
		Scope: "application:application", PackageManager: domain, Binding: domain, Filesystem: domain,
		Environment: domain, Exports: domain, Capabilities: domain,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return fresh, dag
}

func TestPortableRuntimePayloadsFreshLocksCompleteSelectionBeforeExtraction(t *testing.T) {
	fresh, dag := playwrightRuntimePayloadDAGForTest(t)
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(fresh.Closures)
	if err != nil {
		t.Fatal(err)
	}
	wheel := fresh.Plan.Tools[0].Responsibilities.BindingArtifacts[0]
	var source *toolcatalog.EmbeddedPortableToolArtifactSourceV1
	for index := range records.Artifacts {
		if records.Artifacts[index].Artifact == wheel.Reference {
			source = &records.Artifacts[index]
		}
	}
	if source == nil {
		t.Fatal("selected wheel has no embedded source")
	}
	other := []providers.PortableToolArtifactAcquisitionInputV1{{
		Scope: fresh.Plan.Tools[0].Scope, Tool: fresh.Plan.Tools[0].Provenance.Tool,
		Artifact: wheel.Reference, Descriptor: source.Descriptor, Source: source.Source,
		Provenance: providerstore.AcquisitionProvenance{Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: source.Source.Reference.ID},
	}}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, archives := stubPortableRuntimeArchives(t, 0)
	previous := acquirePortableRuntimePayloadV1
	t.Cleanup(func() { acquirePortableRuntimePayloadV1 = previous })
	acquired := 0
	acquirePortableRuntimePayloadV1 = func(_ context.Context, _ providerstore.Store, request providerstore.AcquisitionRequest) (providerstore.AcquisitionResult, error) {
		acquired++
		if !strings.Contains(request.Artifact.LogicalPath, "chromium") && !strings.Contains(request.Artifact.LogicalPath, "ffmpeg") {
			t.Fatalf("acquired non-payload artifact %s", request.Artifact.LogicalPath)
		}
		return providerstore.AcquisitionResult{Artifact: request.Artifact, Provenance: providerstore.AcquisitionProvenance{
			Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: request.Source.ID,
		}}, nil
	}
	result, err := MaterializePortableRuntimePayloadsFreshV1(context.Background(), store, dag, fresh.Closures, other)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Cleanup()
	if acquired != 3 || len(*archives) != 3 || len(result.Lock.Acquisitions) != 4 {
		t.Fatalf("acquired %d, archives %d, locked artifacts %d", acquired, len(*archives), len(result.Lock.Acquisitions))
	}
	if err := providers.ValidatePortableToolLockV1(result.Lock); err != nil {
		t.Fatal(err)
	}
}

func TestPortableRuntimePayloadsFreshRequiresBindingHandoffBeforePayloadAcquisition(t *testing.T) {
	fresh, dag := playwrightRuntimePayloadDAGForTest(t)
	previous := acquirePortableRuntimePayloadV1
	t.Cleanup(func() { acquirePortableRuntimePayloadV1 = previous })
	calls := 0
	acquirePortableRuntimePayloadV1 = func(_ context.Context, _ providerstore.Store, _ providerstore.AcquisitionRequest) (providerstore.AcquisitionResult, error) {
		calls++
		return providerstore.AcquisitionResult{}, errors.New("unexpected acquisition")
	}
	if _, err := MaterializePortableRuntimePayloadsFreshV1(context.Background(), providerstore.Store{}, dag, fresh.Closures, nil); err == nil ||
		!strings.Contains(err.Error(), "requires exactly one verified acquisition") {
		t.Fatalf("missing selected wheel handoff: %v", err)
	}
	if calls != 0 {
		t.Fatalf("acquired %d payloads before binding eligibility", calls)
	}
}

func stubPortableRuntimeArchives(t *testing.T, failAt int) (*int, *[]providerstore.ArchiveMaterializationRequest) {
	t.Helper()
	previousOpen, previousMaterialize := openVerifiedPortableRuntimePayloadV1, materializePortableRuntimeArchiveV1
	t.Cleanup(func() {
		openVerifiedPortableRuntimePayloadV1, materializePortableRuntimeArchiveV1 = previousOpen, previousMaterialize
	})
	opened := 0
	requests := []providerstore.ArchiveMaterializationRequest{}
	openVerifiedPortableRuntimePayloadV1 = func(_ providerstore.Store, _ providerstore.ArtifactDescriptor) (io.Closer, error) {
		opened++
		if opened == failAt {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(strings.NewReader("")), nil
	}
	materializePortableRuntimeArchiveV1 = func(_ context.Context, _ providerstore.Store, request providerstore.ArchiveMaterializationRequest) (providerstore.ArchiveMaterializationResult, error) {
		requests = append(requests, request)
		if failAt < 0 && len(requests) == -failAt {
			return providerstore.ArchiveMaterializationResult{}, errors.New("extraction interrupted")
		}
		observed := make([]providerstore.ArchiveMaterializationEntry, 0, len(request.ExecutablePaths))
		for _, executable := range request.ExecutablePaths {
			relative := executable
			if request.ArchiveRoot != "." && request.ArchiveRoot == request.InstallDirectory {
				relative = strings.TrimPrefix(executable, request.ArchiveRoot+"/")
			}
			file := filepath.Join(request.DestinationRoot, request.InstallDirectory, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return providerstore.ArchiveMaterializationResult{}, err
			}
			if err := os.WriteFile(file, []byte("test executable"), 0o755); err != nil {
				return providerstore.ArchiveMaterializationResult{}, err
			}
			observed = append(observed, providerstore.ArchiveMaterializationEntry{
				ArchivePath: executable, DestinationPath: relative, Kind: providerstore.ArchiveEntryKindRegular,
				Size: strconv.Itoa(len("test executable")),
			})
		}
		return providerstore.ArchiveMaterializationResult{
			FinalPath: filepath.Join(request.DestinationRoot, request.InstallDirectory), ObservedEntries: observed,
		}, nil
	}
	return &opened, &requests
}

func TestPortableRuntimePayloadsLockedMaterializesCompleteSelectedSet(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	opened, archives := stubPortableRuntimeArchives(t, 0)
	previousAcquire, previousRecords := acquirePortableRuntimePayloadV1, embeddedPortableRuntimeLockRecordsV1
	t.Cleanup(func() {
		acquirePortableRuntimePayloadV1, embeddedPortableRuntimeLockRecordsV1 = previousAcquire, previousRecords
	})
	acquirePortableRuntimePayloadV1 = func(context.Context, providerstore.Store, providerstore.AcquisitionRequest) (providerstore.AcquisitionResult, error) {
		t.Fatal("locked replay contacted an acquisition source")
		return providerstore.AcquisitionResult{}, nil
	}
	embeddedPortableRuntimeLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		t.Fatal("locked replay loaded embedded catalog records")
		return toolcatalog.EmbeddedPortableToolLockRecordSetV1{}, nil
	}
	result, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Cleanup()
	if *opened != 3 || len(*archives) != 3 || len(result.copies) != 3 {
		t.Fatalf("opened %d, archives %d, copies %d; want three coupled payloads", *opened, len(*archives), len(result.copies))
	}
	for _, request := range *archives {
		if request.Format != providerstore.ArchiveFormatZip || request.ArchiveRoot == "" || request.ExpectedEntryCount == "" ||
			request.ExpectedUnpackedSize == "" || len(request.ExecutablePaths) == 0 ||
			!strings.Contains(filepath.ToSlash(request.DestinationRoot), "rootfs/opt/reploy/tools/playwright/ms-playwright") {
			t.Fatalf("selected archive contract was not retained: %#v", request)
		}
	}
	if len(result.Environment) != 3 || result.Environment[0].Name != "PLAYWRIGHT_BROWSERS_PATH" ||
		result.Environment[0].Value != "/opt/reploy/tools/playwright/ms-playwright" ||
		result.Environment[1].Name != "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD" || result.Environment[1].Value != "1" ||
		result.Environment[2].Name != "PLAYWRIGHT_SKIP_BROWSER_GC" || result.Environment[2].Value != "1" {
		t.Fatalf("runtime environment = %#v", result.Environment)
	}
	inventory, err := collectPortableRuntimeInventoryV1(result)
	if err != nil || len(inventory) == 0 {
		t.Fatalf("staged runtime inventory = %#v, %v", inventory, err)
	}
	dockerfile, err := result.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(dockerfile), "COPY --chown=0:0 --chmod=a=rX") != 3 ||
		strings.Count(string(dockerfile), "COPY --chown=0:0 --chmod=0555") != 3 ||
		strings.Count(string(dockerfile), "RUN --network=none [\"/bin/chmod\",\"-R\",\"a=rX\",") != 1 ||
		strings.Contains(string(dockerfile), "playwright install") ||
		!strings.Contains(string(dockerfile), "ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=\"1\"") {
		t.Fatalf("runtime payload layer = %s", dockerfile)
	}
}

func TestPortableRuntimePayloadDockerfileEscapesSelectedDollarValue(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	stubPortableRuntimeArchives(t, 0)
	result, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Cleanup()
	variable := result.authorityEnvironment[0]
	variable.Value = "$HOME"
	result.authorityEnvironment[0] = variable
	result.Environment[0] = variable
	for index := range result.authorityLock.Plan.PortableToolPlan.Tools {
		runtime := result.authorityLock.Plan.PortableToolPlan.Tools[index].Runtime
		if runtime == nil {
			continue
		}
		for environmentIndex := range runtime.Environment {
			if runtime.Environment[environmentIndex].Name == variable.Name {
				runtime.Environment[environmentIndex] = variable
			}
		}
	}
	result.Lock = providers.ClonePortableToolLockV1(result.authorityLock)
	dockerfile, err := result.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "ENV "+variable.Name+"=\"\\$HOME\"\n") {
		t.Fatalf("selected dollar value was not escaped in Dockerfile: %s", dockerfile)
	}
}

func TestPortableRuntimePayloadsLockedRequiresEntireVerifiedSetBeforeExtraction(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	_, archives := stubPortableRuntimeArchives(t, 2)
	if _, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock); err == nil ||
		!strings.Contains(err.Error(), "absent or changed") {
		t.Fatalf("missing second payload should fail before extraction: %v", err)
	}
	if len(*archives) != 0 {
		t.Fatalf("extracted %d archives before acquisition barrier", len(*archives))
	}
}

func TestPortableRuntimePayloadsLockedCleansInterruptedStaging(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	_, archives := stubPortableRuntimeArchives(t, -2)
	if _, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock); err == nil ||
		!strings.Contains(err.Error(), "extraction interrupted") {
		t.Fatalf("interrupted extraction should fail: %v", err)
	}
	if len(*archives) != 2 {
		t.Fatalf("archive attempts = %d", len(*archives))
	}
	// The workspace is owned by the store and must not be left behind after a
	// failed multi-payload transaction.
	entries, err := os.ReadDir(filepath.Join(fixture.store.Root(), "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "runtime-payloads-") {
			t.Fatalf("staging workspace remained: %s", entry.Name())
		}
	}
}

func TestPortableRuntimePayloadsLockedRejectsSubstitutedAcquisition(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	for index := range fixture.lock.Acquisitions {
		if strings.Contains(fixture.lock.Acquisitions[index].Artifact.ID, "/payloads/") {
			fixture.lock.Acquisitions[index].Descriptor.Size = "1"
			break
		}
	}
	if err := providers.ValidatePortableToolLockV1(fixture.lock); err == nil {
		t.Fatal("substituted descriptor passed lock validation")
	}
	if _, err := MaterializePortableRuntimePayloadsLockedV1(context.Background(), fixture.store, fixture.lock); err == nil {
		t.Fatal("substituted descriptor reached materialization")
	}
}
