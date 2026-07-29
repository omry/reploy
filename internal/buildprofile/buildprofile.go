// Package buildprofile records opt-in, hierarchical build timings.
//
// Callers choose every recorded label. The recorder never captures command
// arguments, paths, environment variables, or backend output.
package buildprofile

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type recorderContextKey struct{}
type parentContextKey struct{}

type spanRecord struct {
	id       int
	parentID int
	name     string
	started  time.Time
	duration time.Duration
	failed   bool
	ended    bool
}

// Recorder collects build spans. It is safe for concurrent use.
type Recorder struct {
	mutex   sync.Mutex
	started time.Time
	now     func() time.Time
	nextID  int
	spans   []spanRecord
}

// Span is one completed profiling span.
type Span struct {
	ID       int
	ParentID int
	Name     string
	Duration time.Duration
	Failed   bool
}

// Profile is an immutable snapshot of a recorder.
type Profile struct {
	Elapsed time.Duration
	Spans   []Span
}

// New starts a build profile.
func New() *Recorder {
	return newRecorder(time.Now)
}

func newRecorder(now func() time.Time) *Recorder {
	return &Recorder{started: now(), now: now}
}

// WithRecorder enables profiling for work derived from ctx.
func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	if ctx == nil || recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderContextKey{}, recorder)
}

// Start records a named child of the active span. The returned end function is
// idempotent. Passing a non-nil error marks the span as failed.
func Start(ctx context.Context, name string) (context.Context, func(error)) {
	if ctx == nil {
		return ctx, func(error) {}
	}
	recorder, _ := ctx.Value(recorderContextKey{}).(*Recorder)
	if recorder == nil {
		return ctx, func(error) {}
	}
	parentID, _ := ctx.Value(parentContextKey{}).(int)
	started := recorder.now()
	recorder.mutex.Lock()
	recorder.nextID++
	id := recorder.nextID
	recorder.spans = append(recorder.spans, spanRecord{
		id: id, parentID: parentID, name: name, started: started,
	})
	index := len(recorder.spans) - 1
	recorder.mutex.Unlock()

	child := context.WithValue(ctx, parentContextKey{}, id)
	var once sync.Once
	return child, func(err error) {
		once.Do(func() {
			recorder.mutex.Lock()
			record := &recorder.spans[index]
			record.duration = recorder.now().Sub(record.started)
			record.failed = err != nil
			record.ended = true
			recorder.mutex.Unlock()
		})
	}
}

// Snapshot returns all completed spans. Active spans are measured at snapshot
// time so a failure path still produces useful output.
func (recorder *Recorder) Snapshot() Profile {
	if recorder == nil {
		return Profile{}
	}
	now := recorder.now()
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	result := Profile{Elapsed: now.Sub(recorder.started), Spans: make([]Span, len(recorder.spans))}
	for index, record := range recorder.spans {
		duration := record.duration
		if !record.ended {
			duration = now.Sub(record.started)
		}
		result.Spans[index] = Span{
			ID: record.id, ParentID: record.parentID, Name: record.name,
			Duration: duration, Failed: record.failed,
		}
	}
	return result
}

// WriteText writes a stable, hierarchical timing report.
func WriteText(output io.Writer, profile Profile) {
	fmt.Fprintf(output, "\nBuild profile: %s\n", formatDuration(profile.Elapsed))
	if len(profile.Spans) == 0 {
		fmt.Fprintln(output, "  no instrumented build work")
		return
	}
	children := make(map[int][]Span)
	for _, span := range profile.Spans {
		children[span.ParentID] = append(children[span.ParentID], span)
	}
	for parent := range children {
		sort.SliceStable(children[parent], func(left, right int) bool {
			if children[parent][left].Duration != children[parent][right].Duration {
				return children[parent][left].Duration > children[parent][right].Duration
			}
			return children[parent][left].ID < children[parent][right].ID
		})
	}
	var write func(int, int)
	write = func(parentID, depth int) {
		for _, span := range children[parentID] {
			status := ""
			if span.Failed {
				status = " (failed)"
			}
			fmt.Fprintf(
				output, "%s%9s  %s%s\n",
				strings.Repeat("  ", depth+1), formatDuration(span.Duration), span.Name, status,
			)
			write(span.ID, depth+1)
			self := span.Duration
			for _, child := range children[span.ID] {
				self -= child.Duration
			}
			if self >= time.Millisecond && len(children[span.ID]) != 0 {
				fmt.Fprintf(
					output, "%s%9s  Other work in %s\n",
					strings.Repeat("  ", depth+2), formatDuration(self), span.Name,
				)
			}
		}
	}
	write(0, 0)
}

func formatDuration(duration time.Duration) string {
	if duration >= time.Minute {
		return duration.Round(100 * time.Millisecond).String()
	}
	if duration >= time.Second {
		return duration.Round(10 * time.Millisecond).String()
	}
	if duration >= time.Millisecond {
		return duration.Round(100 * time.Microsecond).String()
	}
	return duration.Round(time.Microsecond).String()
}
