package buildprofile

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWriteTextShowsHierarchySelfTimeAndFailures(t *testing.T) {
	now := time.Unix(0, 0)
	recorder := newRecorder(func() time.Time { return now })
	ctx := WithRecorder(context.Background(), recorder)
	parentCtx, endParent := Start(ctx, "Resolve Python packages")
	now = now.Add(2 * time.Second)
	_, endChild := Start(parentCtx, "Build wheel: omegaconf")
	now = now.Add(5 * time.Second)
	endChild(errors.New("failed"))
	now = now.Add(3 * time.Second)
	endParent(nil)

	var output bytes.Buffer
	WriteText(&output, recorder.Snapshot())
	got := output.String()
	for _, expected := range []string{
		"Build profile: 10s",
		"10s  Resolve Python packages",
		"5s  Build wheel: omegaconf (failed)",
		"5s  Other work in Resolve Python packages",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("profile output does not contain %q:\n%s", expected, got)
		}
	}
}

func TestStartWithoutRecorderPreservesContext(t *testing.T) {
	ctx := context.Background()
	got, end := Start(ctx, "ignored")
	end(nil)
	if got != ctx {
		t.Fatal("profiling without a recorder changed the context")
	}
}
