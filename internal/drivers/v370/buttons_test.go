package v370drv_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370"
	"github.com/scanSTATION/lib-v370/sim/synth"

	"github.com/scanSTATION/scanSTATION-server/internal/buttons"
	"github.com/scanSTATION/scanSTATION-server/internal/config"
	v370drv "github.com/scanSTATION/scanSTATION-server/internal/drivers/v370"
)

// Watcher + handler against the synthetic scanner (just switched on: the
// dummy firmware is uploaded): Email (no action), then PDF → a lossless PDF
// in the configured directory, then PDF again (second scan, same session).
func TestPDFButtonScans(t *testing.T) {
	dev, err := synth.New(synth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	inj, _ := v370drv.ParseButtons("email,pdf,pdf")
	d := &v370drv.Direct{Open: inj.Wrap(libv370.SyntheticScanner(dev)), Firmware: synth.DummyFirmware(), WorkDir: t.TempDir()}
	defer d.Close()

	out := t.TempDir()
	cfg := config.Default()
	a := cfg.Buttons[config.ButtonPDF]
	a.DPI, a.OutputDir = 100, out // small and fast
	cfg.Buttons[config.ButtonPDF] = a
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	events := make(chan buttons.Event, 4)
	go d.WatchButtons(ctx, 50*time.Millisecond, buttons.Handler(cfg, d.Scan, nil, func(e buttons.Event) { events <- e }))

	first := <-events
	if first.Button != "email" || first.Action != config.ActionNone {
		t.Fatalf("first event %+v", first)
	}
	ev := <-events
	if ev.Err != nil || ev.Button != "pdf" || ev.Backend != v370drv.Name || len(ev.Files) != 2 {
		t.Fatalf("pdf event %+v", ev)
	}
	if again := <-events; again.Err != nil || len(again.Files) != 2 || again.Files[0] == ev.Files[0] {
		t.Fatalf("second pdf event %+v", again)
	}
	pdf, err := os.ReadFile(ev.Files[0])
	if err != nil || filepath.Dir(ev.Files[0]) != out || !bytes.Contains(pdf, []byte("/FlateDecode")) {
		t.Fatalf("pdf %s: %v", ev.Files[0], err)
	}
	if filepath.Ext(ev.Files[1]) != ".json" {
		t.Fatalf("sidecar %v", ev.Files)
	}
}
