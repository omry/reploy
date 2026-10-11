//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdmissionWaitRequiresMatchingRelease(t *testing.T) {
	dir := t.TempDir()
	nonce := strings.Repeat("a", 32)
	finished := make(chan error, 1)
	go func() { finished <- awaitAdmission(dir, nonce, time.Second) }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "probe-admission-ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wait never became ready")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-finished:
		t.Fatalf("passed without release: %v", err)
	default:
	}
	if err := releaseAdmission(dir, []string{strings.Repeat("b", 32)}); err == nil {
		t.Fatal("accepted a foreign nonce")
	}
	if err := releaseAdmission(dir, []string{nonce}); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("admission markers were not removed: %v, %v", entries, err)
	}
}

func TestAdmissionFailsClosed(t *testing.T) {
	nonce := strings.Repeat("a", 32)
	t.Run("timeout", func(t *testing.T) {
		if err := awaitAdmission(t.TempDir(), nonce, time.Millisecond); err == nil {
			t.Fatal("passed without release")
		}
	})
	t.Run("foreign marker", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "probe-admission-release"), []byte("foreign"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := awaitAdmission(dir, nonce, time.Second); err == nil {
			t.Fatal("accepted foreign release marker")
		}
	})
	t.Run("unwritable ready", func(t *testing.T) {
		if err := awaitAdmission(filepath.Join(t.TempDir(), "missing"), nonce, time.Second); err == nil {
			t.Fatal("ignored ready marker failure")
		}
	})
	t.Run("missing ready", func(t *testing.T) {
		if err := releaseAdmission(t.TempDir(), []string{nonce}); err == nil {
			t.Fatal("released a container that never became ready")
		}
	})
	for _, args := range [][]string{nil, {"short"}, {strings.Repeat("z", 32)}, {nonce, "request"}, {nonce, "supervisor", "extra"}} {
		if err := runAdmissionWait(args); err == nil {
			t.Fatalf("accepted invalid wait arguments: %v", args)
		}
	}
	for _, args := range [][]string{nil, {"short"}, {nonce, "extra"}} {
		if err := releaseAdmission(t.TempDir(), args); err == nil {
			t.Fatalf("accepted invalid release arguments: %v", args)
		}
	}
}
