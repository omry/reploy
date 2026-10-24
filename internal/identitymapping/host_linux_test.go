//go:build linux

package identitymapping

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRealLinuxHostRejectionDoesNotCreateDeploymentMetadata(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USER", "forged-authority")
	t.Setenv("LOGNAME", "root")
	_, err := CheckHost(t.Context(), dir, []Interval{{HostStart: 1, Count: 1}}, smallMap())
	if err == nil {
		t.Fatal("accepted named host UID1 or unproved host authority")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("host check changed selected directory: %v %v", entries, err)
	}
}

func TestRealLinuxHostRejectsMissingSymlinkAndCancelledDirectory(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	if _, err := CheckHost(t.Context(), missing, smallMap(), smallMap()); err == nil {
		t.Fatal("accepted missing owner")
	}
	link := filepath.Join(dir, "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckHost(t.Context(), link, smallMap(), smallMap()); err == nil {
		t.Fatal("accepted noncanonical owner")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CheckHost(ctx, dir, smallMap(), smallMap()); err == nil {
		t.Fatal("accepted cancelled check")
	}
	if _, err := CheckHost(nil, dir, smallMap(), smallMap()); err == nil {
		t.Fatal("accepted nil context")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing owner created")
	}
}

func TestHostAuthorityFileReaderRejectsUnsafeOrUnboundedInputs(t *testing.T) {
	dir := t.TempDir()
	large := filepath.Join(dir, "large")
	if err := os.WriteFile(large, []byte(strings.Repeat("x", hostInventoryLimit+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, large, link, filepath.Join(dir, "missing")} {
		if _, err := readHostFile(t.Context(), path); err == nil {
			t.Fatalf("accepted unsafe file %s", path)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readHostFile(ctx, "/etc/passwd"); err == nil {
		t.Fatal("read despite cancellation")
	}
	content, err := readHostFile(t.Context(), "/etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	if content == "" {
		t.Fatal("real inventory read was empty")
	}
	file := filepath.Join(dir, "untrusted")
	if err := os.WriteFile(file, []byte("data\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := readHostFile(t.Context(), file); err == nil {
		t.Fatal("accepted writable authority")
	}
}

func TestBoundedHostFileReadCancellationDoesNotAccumulateOrAcceptLateResults(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var starts atomic.Int32
	operation := func(string) (string, error) {
		if starts.Add(1) == 1 {
			close(started)
		}
		<-release
		close(finished)
		return "late authority", nil
	}
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	type result struct {
		content string
		err     error
	}
	firstContext, cancelFirst := context.WithCancel(t.Context())
	first := make(chan result, 1)
	go func() {
		content, err := readHostFileBounded(firstContext, "authority", operation)
		first <- result{content: content, err: err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("bounded read did not start")
	}
	cancelFirst()
	select {
	case got := <-first:
		if !errors.Is(got.err, context.Canceled) || got.content != "" {
			t.Fatalf("canceled read accepted result: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled read did not return")
	}

	const attempts = 16
	waiting := make(chan result, attempts)
	for i := 0; i < attempts; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		go func(ctx context.Context) {
			content, err := readHostFileBounded(ctx, "authority", operation)
			waiting <- result{content: content, err: err}
		}(ctx)
	}
	for i := 0; i < attempts; i++ {
		select {
		case got := <-waiting:
			if !errors.Is(got.err, context.DeadlineExceeded) || got.content != "" {
				t.Fatalf("waiting canceled read accepted result: %#v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("waiting canceled read did not return")
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("canceled attempts started %d blocked operations", got)
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stalled operation did not finish after release")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	content, err := readHostFileBounded(ctx, "authority", func(string) (string, error) {
		return "fresh authority", nil
	})
	if err != nil || content != "fresh authority" {
		t.Fatalf("permit was not released after late completion: %q, %v", content, err)
	}
}

func TestHostCommandOutputAndTimeAreBounded(t *testing.T) {
	var out boundedOutput
	if _, err := out.Write(make([]byte, hostInventoryLimit)); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("x")); err == nil {
		t.Fatal("output bound not enforced")
	}
	if out.Len() != hostInventoryLimit {
		t.Fatal("overflow changed retained output")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runHostCommand(ctx, "sleep", "10"); err == nil {
		t.Fatal("unbounded command succeeded")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("command cancellation was not bounded")
	}
	ctx2, cancel2 := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel2()
	if _, err := runHostCommand(ctx2, "head", "-c", "1048577", "/dev/zero"); err == nil {
		t.Fatal("oversized command output accepted")
	}
	if _, err := runHostCommand(t.Context(), "reploy-nonexistent-host-utility"); err == nil {
		t.Fatal("missing authority utility accepted")
	}
}

func TestAccountInventoryPreservesGecosHashAndHyphenatedNames(t *testing.T) {
	f := newHostFixture()
	f.lookup = strings.ReplaceAll(f.lookup, "alice:", "alice-test:")
	f.files["/etc/passwd"] = strings.ReplaceAll(f.files["/etc/passwd"], "alice:", "alice-test:")
	f.files["/etc/passwd"] = strings.ReplaceAll(f.files["/etc/passwd"], "Alice:", "Alice #1:")
	f.files["/etc/subuid"] = strings.ReplaceAll(f.files["/etc/subuid"], "alice:", "alice-test:")
	f.files["/etc/subgid"] = strings.ReplaceAll(f.files["/etc/subgid"], "alice:", "alice-test:")
	f.activeUID = strings.ReplaceAll(f.activeUID, "alice ", "alice-test ")
	f.activeGID = strings.ReplaceAll(f.activeGID, "alice ", "alice-test ")
	if _, err := f.check(smallMap(), smallMap()); err != nil {
		t.Fatal(err)
	}
}
