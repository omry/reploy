//go:build windows

package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOperationLockConcurrentFirstAcquirers(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".reploy"), 0o700); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 16)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		for i := 0; i < cap(results); i++ {
			go func() {
				<-start
				lock, err := AcquireOperationLock(ctx, dir)
				if err == nil {
					err = lock.Unlock()
				}
				results <- err
			}()
		}
		close(start)
		for i := 0; i < cap(results); i++ {
			if err := <-results; err != nil {
				t.Errorf("first acquirer attempt %d: %v", attempt, err)
			}
		}
		cancel()
	}
}
