package v370drv_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// pdfPageCount counts page objects in a PDF.
func pdfPageCount(b []byte) int {
	return len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(b, -1))
}

func multiPageSetup(t *testing.T, presses string, timeoutMin float64) (*v370drv.Direct, config.Config, string) {
	d, _, cfg, out := multiPageSetupLED(t, presses, timeoutMin, true)
	return d, cfg, out
}

func multiPageSetupLED(t *testing.T, presses string, timeoutMin float64, ledPatch bool) (*v370drv.Direct, *synth.Device, config.Config, string) {
	t.Helper()
	dev, err := synth.New(synth.Options{LEDPatch: ledPatch})
	if err != nil {
		t.Fatal(err)
	}
	inj, err := v370drv.ParseButtons(presses)
	if err != nil {
		t.Fatal(err)
	}
	inj.Into(dev)
	d := &v370drv.Direct{Open: libv370.SyntheticScanner(dev), Firmware: synth.DummyFirmware(), WorkDir: t.TempDir()}
	out := t.TempDir()
	cfg := config.Default()
	a := cfg.Buttons[config.ButtonPDF]
	a.DPI, a.OutputDir, a.MultiTimeoutMin = 100, out, timeoutMin
	cfg.Buttons[config.ButtonPDF] = a
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return d, dev, cfg, out
}

// Long press: start a document; further presses of the same button (short
// or long) add pages; other buttons are ignored; a short Power press
// finishes it into one PDF. A short press afterwards is a single scan.
func TestPDFButtonMultiPage(t *testing.T) {
	d, cfg, out := multiPageSetup(t, "pdf:2s,pdf,pdf:2s,email,power,pdf", 0)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	events := make(chan buttons.Event, 8)
	ctl := buttons.New(cfg, d.Scan, nil, func(e buttons.Event) { events <- e })
	go d.WatchButtons(ctx, 50*time.Millisecond, ctl.OnPress)

	for i, want := range []struct {
		kind  string
		pages int
	}{{buttons.KindStart, 1}, {buttons.KindPage, 2}, {buttons.KindPage, 3}} {
		ev := <-events
		if ev.Err != nil || ev.Kind != want.kind || ev.Pages != want.pages {
			t.Fatalf("event %d: %+v, want %s with %d pages", i, ev, want.kind, want.pages)
		}
		if i == 0 && (ev.Held < 1500*time.Millisecond || ev.Held > 1800*time.Millisecond) {
			t.Fatalf("document must start at 1.5 s while held, not %v", ev.Held)
		}
		if docs := ctl.Documents(); len(docs) != 1 || docs[0].Pages != want.pages {
			t.Fatalf("documents %+v", docs)
		}
	}
	if ev := <-events; ev.Button != "email" || ev.Kind != "" || ev.Err != nil {
		t.Fatalf("email while a document is open must be ignored: %+v", ev)
	}
	fin := <-events
	if fin.Err != nil || fin.Button != "pdf" || fin.Kind != buttons.KindFinish || fin.Pages != 3 || len(fin.Files) != 2 {
		t.Fatalf("finish %+v", fin)
	}
	pdf, err := os.ReadFile(fin.Files[0])
	if err != nil || filepath.Dir(fin.Files[0]) != out || pdfPageCount(pdf) != 3 {
		t.Fatalf("document %s: %v, %d pages", fin.Files[0], err, pdfPageCount(pdf))
	}
	side, _ := os.ReadFile(fin.Files[1])
	if !bytes.Contains(side, []byte(`"pages": 3`)) || !bytes.Contains(side, []byte(`"finished_by": "finish"`)) {
		t.Fatalf("sidecar %s", side)
	}
	if len(ctl.Documents()) != 0 {
		t.Fatal("document still open")
	}
	if single := <-events; single.Err != nil || single.Kind != buttons.KindSingle || len(single.Files) != 2 {
		t.Fatalf("single after finish %+v", single)
	}
	// Only the document, its sidecar and the single scan (+ sidecar) remain.
	if m, _ := filepath.Glob(filepath.Join(out, "*")); len(m) != 4 {
		t.Fatalf("output files %v", m)
	}
}

// The same works for any button configured to scan to PDF (here Email).
func TestEmailButtonMultiPage(t *testing.T) {
	d, cfg, _ := multiPageSetup(t, "email:2s,email,power", 0)
	defer d.Close()
	out := t.TempDir()
	cfg.Buttons[config.ButtonEmail] = config.ButtonAction{Action: config.ActionScan, Format: "pdf", DPI: 100, Size: "a4",
		OutputDir: out, Prefix: "mail_{datetime}"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	events := make(chan buttons.Event, 4)
	go d.WatchButtons(ctx, 50*time.Millisecond, buttons.Handler(cfg, d.Scan, nil, func(e buttons.Event) { events <- e }))
	for _, kind := range []string{buttons.KindStart, buttons.KindPage, buttons.KindFinish} {
		if ev := <-events; ev.Err != nil || ev.Button != "email" || ev.Kind != kind {
			t.Fatalf("%+v, want %s", ev, kind)
		}
	}
	m, _ := filepath.Glob(filepath.Join(out, "mail_*.pdf"))
	if len(m) != 1 {
		t.Fatalf("documents %v", m)
	}
	if b, _ := os.ReadFile(m[0]); pdfPageCount(b) != 2 {
		t.Fatalf("%s: %d pages", m[0], pdfPageCount(b))
	}
}

// An open document is finished after the idle timeout.
func TestPDFButtonMultiPageTimeout(t *testing.T) {
	d, cfg, out := multiPageSetup(t, "pdf:2s", 0.05) // 3 s
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	events := make(chan buttons.Event, 4)
	ctl := buttons.New(cfg, d.Scan, nil, func(e buttons.Event) { events <- e })
	go d.WatchButtons(ctx, 50*time.Millisecond, ctl.OnPress)
	if ev := <-events; ev.Err != nil || ev.Kind != buttons.KindStart {
		t.Fatalf("start %+v", ev)
	}
	select {
	case ev := <-events:
		if ev.Err != nil || ev.Kind != buttons.KindTimeout || ev.Pages != 1 || filepath.Dir(ev.Files[0]) != out {
			t.Fatalf("timeout %+v", ev)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("document not finished by the timeout")
	}
}

// The scanner's light follows the documents: single page → brief off;
// document → orange with brief off per page; Power → brief off, green.
// Without the LED patch (buttons pause while orange) the light stays green
// and orange only flashes.
func TestButtonLight(t *testing.T) {
	for _, tc := range []struct {
		patch bool
		want  string
	}{
		// single: off, green | start: off, flash orange, off, orange |
		// page: off, orange | Power: off, green
		{true, "[off green off orange off orange off orange off green]"},
		// stock: "off" shows green, documents stay green, orange only flashes
		{false, "[green green green orange green green green green green green]"},
	} {
		d, dev, cfg, _ := multiPageSetupLED(t, "pdf,pdf:2s,pdf,power", 0, tc.patch)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		events := make(chan buttons.Event, 8)
		ctl := buttons.New(cfg, d.Scan, nil, func(e buttons.Event) { events <- e })
		ctl.SetIndicator(d)
		go d.WatchButtons(ctx, 50*time.Millisecond, ctl.OnPress)
		for _, kind := range []string{buttons.KindSingle, buttons.KindStart, buttons.KindPage, buttons.KindFinish} {
			if ev := <-events; ev.Err != nil || ev.Kind != kind {
				t.Fatalf("patch %v: %+v, want %s", tc.patch, ev, kind)
			}
		}
		// 3 scans (single, document start, next page); with the patch each
		// image pass blinks fast.
		if n, want := dev.FastImagePasses(), map[bool]int{true: 3, false: 0}[tc.patch]; n != want {
			t.Errorf("patch %v: %d fast image passes, want %d", tc.patch, n, want)
		}
		if got := fmt.Sprint(dev.LEDs()); got != tc.want {
			t.Errorf("patch %v: light %s, want %s", tc.patch, got, tc.want)
		}
		cancel()
		d.Close()
	}
}

// Released after the orange flash (1 s) but before 1.5 s: cancelled.
func TestButtonCancel(t *testing.T) {
	d, dev, cfg, out := multiPageSetupLED(t, "pdf:1200ms", 0, true)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	events := make(chan buttons.Event, 2)
	ctl := buttons.New(cfg, d.Scan, nil, func(e buttons.Event) { events <- e })
	ctl.SetIndicator(d)
	go d.WatchButtons(ctx, 50*time.Millisecond, ctl.OnPress)
	if ev := <-events; ev.Kind != buttons.KindCancel || ev.Err != nil || len(ev.Files) != 0 {
		t.Fatalf("%+v", ev)
	}
	if got := fmt.Sprint(dev.LEDs()); got != "[off orange off green]" {
		t.Fatalf("light %s", got)
	}
	if m, _ := filepath.Glob(filepath.Join(out, "*")); len(m) != 0 || len(ctl.Documents()) != 0 {
		t.Fatalf("files %v, documents %v", m, ctl.Documents())
	}
}
