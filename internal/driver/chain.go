package driver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// Chain is the auto backend: several drivers in priority order. Status,
// device listing and buttons come from the first driver; a scan goes to
// the first driver that supports it. Hardware errors are never retried
// with another driver: a half-finished session must not be handed to
// another program.
type Chain struct {
	Backends []Backend
	Log      *slog.Logger
}

var _ Backend = (*Chain)(nil)

// Name implements Backend.
func (c *Chain) Name() string { return Auto }

// ListDevices implements Backend (first driver).
func (c *Chain) ListDevices(ctx context.Context) ([]Device, error) {
	return c.Backends[0].ListDevices(ctx)
}

// Status implements Backend (first driver).
func (c *Chain) Status(ctx context.Context) (Status, error) { return c.Backends[0].Status(ctx) }

// Scan implements Backend.
func (c *Chain) Scan(ctx context.Context, req scan.ScanRequest) (Result, error) {
	var reasons []string
	for i, b := range c.Backends {
		if i > 0 && c.Log != nil {
			c.Log.Info("falling back to the next driver", "driver", b.Name(), "reason", reasons[len(reasons)-1])
		}
		res, err := c.scanWith(ctx, i, req)
		if err == nil || !IsUnsupported(err) {
			return res, err
		}
		reasons = append(reasons, b.Name()+": "+err.Error())
	}
	return Result{}, fmt.Errorf("%w by any compiled-in driver (%s)", ErrUnsupported, strings.Join(reasons, "; "))
}

// scanWith scans with driver i; the drivers before it that reserve the
// scanner lend it for the duration.
func (c *Chain) scanWith(ctx context.Context, i int, req scan.ScanRequest) (Result, error) {
	for _, other := range c.Backends[:i] {
		if s, ok := other.(Suspender); ok {
			s.Suspend()
			defer s.Resume()
		}
	}
	return c.Backends[i].Scan(ctx, req)
}

// WatchButtons implements ButtonWatcher (first driver that watches buttons).
func (c *Chain) WatchButtons(ctx context.Context, interval time.Duration, onPress func(context.Context, Button)) error {
	for _, b := range c.Backends {
		if w, ok := b.(ButtonWatcher); ok {
			return w.WatchButtons(ctx, interval, onPress)
		}
	}
	return errors.New("no compiled-in driver can watch buttons")
}

// Close closes every driver that holds resources.
func (c *Chain) Close() error {
	closeAll(c.Backends)
	return nil
}
