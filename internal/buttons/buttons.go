// Package buttons turns scanner button presses into configured actions:
// scan and store the file in a configured directory. Every button scanning
// to PDF (PDF, Email, Printer — per configuration) can also build
// multi-page documents:
//
//	idle:           short press → single-page scan
//	                long press  → start a document, scan page 1
//	document open:  press of the same button → scan the next page
//	                short press of Power     → finish: one PDF from all pages
//
// One document is open at a time; other buttons are ignored meanwhile. An
// open document is finished automatically after an idle timeout (and when
// the controller is closed), so no pages are lost.
//
// With a driver.Indicator the scanner's light shows what happens:
//
//	single-page scan:         green → briefly off → green
//	document started:         off → orange (stays while the document is open)
//	next page:                orange → briefly off → orange
//	finished (Power/timeout): briefly off → green
//	scan failed:              3 quick off/on in the current colour
//
// Without an off state (stock firmware, where buttons also pause while the
// light is orange) the light stays green during documents and orange only
// flashes briefly.
package buttons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/config"
	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	"github.com/scanSTATION/scanSTATION-server/internal/files"
	"github.com/scanSTATION/scanSTATION-server/internal/pdfutil"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// ScanFunc performs one scan (e.g. driver.Scan, or the API server's
// serialised scan).
type ScanFunc func(ctx context.Context, req scan.ScanRequest) (driver.Result, error)

// Kinds of events.
const (
	KindSingle  = "single"  // single-page scan stored
	KindStart   = "start"   // document started, page 1 scanned
	KindPage    = "page"    // next page scanned
	KindFinish  = "finish"  // document finished (Power)
	KindTimeout = "timeout" // document finished after the idle timeout
)

// Event describes what happened for a press (for logs and tests).
type Event struct {
	Button  string
	Action  string
	Kind    string        // see Kind*; empty if nothing was scanned
	Held    time.Duration // press duration (0: unknown)
	Pages   int           // pages of the document so far / in total
	Files   []string
	Backend string
	Err     error
}

// Document describes an open multi-page document (status API).
type Document struct {
	Button  string    `json:"button"`
	Pages   int       `json:"pages"`
	Started time.Time `json:"started"`
	// FinishesAt is when the idle timeout finishes it.
	FinishesAt time.Time `json:"finishes_at"`
}

type document struct {
	button     string
	action     config.ButtonAction
	dir        string
	prefix     string
	started    time.Time
	finishesAt time.Time
	pages      []driver.Result
	timer      *time.Timer
}

// Controller handles presses (driver.ButtonWatcher's onPress).
type Controller struct {
	cfg    config.Config
	scanFn ScanFunc
	log    *slog.Logger
	notify func(Event)

	mu   sync.Mutex
	docs map[string]*document
	ind  driver.Indicator
}

// Light feedback timing.
const (
	blinkTime = 300 * time.Millisecond
	errorTime = 150 * time.Millisecond
)

// SetIndicator makes the controller show its state on the scanner's light.
func (c *Controller) SetIndicator(i driver.Indicator) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ind = i
}

func (c *Controller) light(ctx context.Context, l driver.Light) {
	if c.ind == nil {
		return
	}
	if err := c.ind.SetLight(ctx, l); err != nil {
		c.log.Debug("status light", "err", err)
	}
}

// off is the state that stands for "off": off itself, or the other colour.
func (c *Controller) off(ctx context.Context, base driver.Light) driver.Light {
	if c.ind != nil && c.ind.LightOffSupported(ctx) {
		return driver.LightOff
	}
	if base == driver.LightOrange {
		return driver.LightGreen
	}
	return driver.LightOrange
}

// flash switches the light to "off" and back to base n times.
func (c *Controller) flash(ctx context.Context, base driver.Light, n int, d time.Duration) {
	if c.ind == nil {
		return
	}
	other := c.off(ctx, base)
	for i := 0; i < n; i++ {
		c.light(ctx, other)
		time.Sleep(d)
		c.light(ctx, base)
		if i+1 < n {
			time.Sleep(d)
		}
	}
}

// docLight is the light while a document is open: orange, but only if the
// firmware keeps the buttons working while orange (LED patch); else green.
func (c *Controller) docLight(ctx context.Context) driver.Light {
	if c.ind != nil && c.ind.LightOffSupported(ctx) {
		return driver.LightOrange
	}
	return driver.LightGreen
}

// settle switches through "off" to l (document started / finished).
func (c *Controller) settle(ctx context.Context, l driver.Light) {
	if c.ind == nil {
		return
	}
	if o := c.off(ctx, l); o == driver.LightOff {
		c.light(ctx, o)
		time.Sleep(blinkTime)
	}
	c.light(ctx, l)
}

// New creates a controller. notify (optional) receives an Event after each
// press and after a timeout.
func New(cfg config.Config, scanFn ScanFunc, log *slog.Logger, notify func(Event)) *Controller {
	if log == nil {
		log = slog.Default()
	}
	return &Controller{cfg: cfg, scanFn: scanFn, log: log, notify: notify, docs: map[string]*document{}}
}

// Handler returns a controller's press callback (convenience for callers
// that need no document status).
func Handler(cfg config.Config, scanFn ScanFunc, log *slog.Logger, notify func(Event)) func(context.Context, driver.Press) {
	return New(cfg, scanFn, log, notify).OnPress
}

// Documents lists the open documents.
func (c *Controller) Documents() []Document {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []Document{}
	for _, d := range c.docs {
		out = append(out, Document{Button: d.button, Pages: len(d.pages), Started: d.started, FinishesAt: d.finishesAt})
	}
	return out
}

// Close finishes all open documents (e.g. at shutdown).
func (c *Controller) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, d := range c.docs {
		c.emit(c.finishLocked(d, KindFinish))
		delete(c.docs, name)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		c.light(ctx, driver.LightGreen)
		cancel()
	}
}

func (c *Controller) emit(ev Event) {
	if c.notify != nil {
		c.notify(ev)
	}
}

// OnPress handles one press.
func (c *Controller) OnPress(ctx context.Context, p driver.Press) {
	b := string(p.Button)
	ev := Event{Button: b, Action: config.ActionNone, Held: p.Held}
	defer func() { c.emit(ev) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	open := c.openLocked()
	if p.Button == driver.ButtonPower {
		if open == nil {
			c.log.Info("power button pressed (hold it ~3 s to switch the scanner off)")
			return
		}
		ev = c.finishLocked(open, KindFinish)
		ev.Held = p.Held
		delete(c.docs, open.button)
		c.settle(ctx, driver.LightGreen)
		if ev.Err != nil {
			c.log.Error("multi-page document failed", "button", open.button, "err", ev.Err)
			c.flash(ctx, driver.LightGreen, 3, errorTime)
		}
		return
	}
	if open != nil && open.button != b {
		c.log.Info("multi-page document open: ignoring this button; press "+open.button+" for the next page, Power to finish",
			"button", b, "pages", len(open.pages))
		return
	}
	a, ok := c.cfg.Buttons[b]
	if !ok || a.Action != config.ActionScan {
		c.log.Info("button pressed, no action configured", "button", b)
		return
	}
	ev.Action = config.ActionScan
	long := a.MultiPageEnabled() && p.Held >= a.LongPress()

	d := c.docs[b]
	switch {
	case d == nil && !long:
		ev.Kind = KindSingle
		c.flash(ctx, driver.LightGreen, 1, blinkTime)
		ev.Files, ev.Backend, ev.Err = c.single(ctx, b, a)
		if ev.Err == nil {
			c.log.Info("button scan stored", "button", b, "file", ev.Files[0], "backend", ev.Backend)
		}
	case d == nil:
		ev.Kind = KindStart
		if d, ev.Err = c.start(b, a); ev.Err != nil {
			break
		}
		c.settle(ctx, c.docLight(ctx))
		ev.Backend, ev.Err = c.page(ctx, d)
		ev.Pages = len(d.pages)
		if ev.Err != nil { // no page: no document
			d.timer.Stop()
			delete(c.docs, b)
			c.light(ctx, driver.LightGreen)
			break
		}
		c.log.Info("multi-page document started: press "+b+" for the next page, Power (short) to finish",
			"button", b, "pages", ev.Pages, "auto_finish_after", a.MultiTimeout())
	default:
		ev.Kind = KindPage
		c.flash(ctx, c.docLight(ctx), 1, blinkTime)
		ev.Backend, ev.Err = c.page(ctx, d)
		ev.Pages = len(d.pages)
		if ev.Err == nil {
			c.log.Info("multi-page document: page scanned", "button", b, "pages", ev.Pages)
		}
	}
	if ev.Err != nil {
		c.log.Error("button scan failed", "button", b, "kind", ev.Kind, "err", ev.Err)
		base := driver.LightGreen
		if c.docs[b] != nil {
			base = c.docLight(ctx)
		}
		c.flash(ctx, base, 3, errorTime)
	}
}

// openLocked returns the open document, if any.
func (c *Controller) openLocked() *document {
	for _, d := range c.docs {
		return d
	}
	return nil
}

// single scans one page straight into the button's directory.
func (c *Controller) single(ctx context.Context, b string, a config.ButtonAction) ([]string, string, error) {
	post, err := c.cfg.ButtonPipeline(b)
	if err != nil { // validated at load; defensive
		return nil, "", err
	}
	req, err := a.Request(time.Now())
	if err != nil {
		return nil, "", err
	}
	req.Post = post
	dir, err := prepareDir(a)
	if err != nil {
		return nil, "", err
	}
	req.FileNamePrefix = uniquePrefix(dir, req.FileNamePrefix)
	res, err := c.scanFn(ctx, req)
	if err != nil {
		return nil, "", err
	}
	moved, err := moveAll(res.Files(), dir)
	return moved, res.Backend, err
}

// start opens a document; its name is fixed now ({datetime} = start time).
func (c *Controller) start(b string, a config.ButtonAction) (*document, error) {
	now := time.Now()
	req, err := a.Request(now)
	if err != nil {
		return nil, err
	}
	dir, err := prepareDir(a)
	if err != nil {
		return nil, err
	}
	d := &document{button: b, action: a, dir: dir, prefix: uniquePrefix(dir, req.FileNamePrefix), started: now}
	d.timer = time.AfterFunc(a.MultiTimeout(), func() { c.timeout(b, d) })
	d.finishesAt = now.Add(a.MultiTimeout())
	c.docs[b] = d
	return d, nil
}

// page scans the next page of d (kept in the driver's work directory until
// the document is finished) and restarts the idle timeout.
func (c *Controller) page(ctx context.Context, d *document) (string, error) {
	post, err := c.cfg.ButtonPipeline(d.button)
	if err != nil {
		return "", err
	}
	req, err := d.action.Request(time.Now())
	if err != nil {
		return "", err
	}
	req.Post = post
	req.FileNamePrefix = fmt.Sprintf("%s_page%03d", d.prefix, len(d.pages)+1)
	res, err := c.scanFn(ctx, req)
	d.timer.Reset(d.action.MultiTimeout())
	d.finishesAt = time.Now().Add(d.action.MultiTimeout())
	if err != nil {
		return "", err
	}
	d.pages = append(d.pages, res)
	return res.Backend, nil
}

func (c *Controller) timeout(b string, d *document) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.docs[b] != d {
		return // finished or replaced meanwhile
	}
	delete(c.docs, b)
	ev := c.finishLocked(d, KindTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c.settle(ctx, driver.LightGreen)
	c.emit(ev)
}

// finishLocked merges the pages into <prefix>.pdf (and the originals into
// <prefix>-original.pdf), writes <prefix>.json with every page's sidecar
// and removes the page files.
func (c *Controller) finishLocked(d *document, kind string) Event {
	d.timer.Stop()
	ev := Event{Button: d.button, Action: config.ActionScan, Kind: kind, Pages: len(d.pages)}
	defer func() {
		for _, r := range d.pages {
			for _, f := range r.Files() {
				os.Remove(f)
			}
		}
	}()
	if len(d.pages) == 0 {
		ev.Err = errors.New("document has no pages")
		return ev
	}
	ev.Backend = d.pages[0].Backend
	var pdfs, originals []string
	sides := []json.RawMessage{}
	for _, r := range d.pages {
		pdfs = append(pdfs, r.Path)
		for _, f := range r.Extra {
			switch {
			case strings.HasSuffix(f, "-original.pdf"):
				originals = append(originals, f)
			case strings.HasSuffix(f, ".json"):
				if b, err := os.ReadFile(f); err == nil && json.Valid(b) {
					sides = append(sides, b)
				}
			}
		}
	}
	out := filepath.Join(d.dir, d.prefix+".pdf")
	if err := pdfutil.CombinePDFs(pdfs, out); err != nil {
		ev.Err = fmt.Errorf("combining %d pages: %w", len(pdfs), err)
		return ev
	}
	ev.Files = []string{out}
	meta := map[string]any{"file": filepath.Base(out), "pages": len(pdfs), "started": d.started.UTC(),
		"finished": time.Now().UTC(), "finished_by": kind, "page_sidecars": sides}
	if len(originals) > 0 && len(originals) == len(pdfs) {
		orig := filepath.Join(d.dir, d.prefix+"-original.pdf")
		if err := pdfutil.CombinePDFs(originals, orig); err != nil {
			ev.Err = fmt.Errorf("combining the originals: %w", err)
			return ev
		}
		meta["original_file"] = filepath.Base(orig)
		ev.Files = append(ev.Files, orig)
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	side := filepath.Join(d.dir, d.prefix+".json")
	if err := os.WriteFile(side, append(mb, '\n'), 0o644); err != nil {
		ev.Err = err
		return ev
	}
	ev.Files = append(ev.Files, side)
	c.log.Info("multi-page document stored", "button", d.button, "file", out, "pages", len(pdfs), "finished_by", kind)
	return ev
}

func prepareDir(a config.ButtonAction) (string, error) {
	dir, err := a.Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return dir, nil
}

func moveAll(paths []string, dir string) ([]string, error) {
	var moved []string
	for _, f := range paths {
		p, err := files.MoveScannedFile(f, dir, "")
		if err != nil {
			return moved, err
		}
		moved = append(moved, p)
	}
	return moved, nil
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
