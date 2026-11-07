// Package buttons turns scanner button presses into configured actions
// (currently: scan and store the file in a configured directory).
package buttons

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/config"
	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	"github.com/scanSTATION/scanSTATION-server/internal/files"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// ScanFunc performs one scan (e.g. driver.Scan, or the API server's
// serialised scan).
type ScanFunc func(ctx context.Context, req scan.ScanRequest) (driver.Result, error)

// Event describes what happened for a press (for logs and tests).
type Event struct {
	Button  string
	Action  string
	Files   []string
	Backend string
	Err     error
}

// Handler returns the onPress callback for driver.ButtonWatcher. notify
// (optional) receives an Event after each press.
func Handler(cfg config.Config, scanFn ScanFunc, log *slog.Logger, notify func(Event)) func(context.Context, driver.Button) {
	if log == nil {
		log = slog.Default()
	}
	return func(ctx context.Context, b driver.Button) {
		ev := Event{Button: string(b), Action: config.ActionNone}
		defer func() {
			if notify != nil {
				notify(ev)
			}
		}()
		if b == driver.ButtonPower {
			log.Info("power button pressed (hold it ~3 s to switch the scanner off)")
			return
		}
		a, ok := cfg.Buttons[string(b)]
		if !ok || a.Action != config.ActionScan {
			log.Info("button pressed, no action configured", "button", string(b))
			return
		}
		ev.Action = config.ActionScan
		ev.Files, ev.Backend, ev.Err = run(ctx, a, scanFn)
		if ev.Err != nil {
			log.Error("button scan failed", "button", string(b), "err", ev.Err)
			return
		}
		log.Info("button scan stored", "button", string(b), "file", ev.Files[0], "backend", ev.Backend)
	}
}

func run(ctx context.Context, a config.ButtonAction, scanFn ScanFunc) ([]string, string, error) {
	req, err := a.Request(time.Now())
	if err != nil {
		return nil, "", err
	}
	dir, err := a.Dir()
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("creating %s: %w", dir, err)
	}
	req.FileNamePrefix = uniquePrefix(dir, req.FileNamePrefix)
	res, err := scanFn(ctx, req)
	if err != nil {
		return nil, "", err
	}
	var moved []string
	for _, f := range res.Files() {
		p, err := files.MoveScannedFile(f, dir, "")
		if err != nil {
			return moved, res.Backend, err
		}
		moved = append(moved, p)
	}
	return moved, res.Backend, nil
}

// uniquePrefix returns prefix, or prefix-2, prefix-3, … so that no file in
// dir starts with it: two presses within the same second (same {datetime})
// must not overwrite each other's scan.
func uniquePrefix(dir, prefix string) string {
	p := prefix
	for n := 2; ; n++ {
		if m, _ := filepath.Glob(filepath.Join(dir, p+".*")); len(m) == 0 {
			return p
		}
		p = fmt.Sprintf("%s-%d", prefix, n)
	}
}
