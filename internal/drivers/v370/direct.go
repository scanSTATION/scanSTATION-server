// Package v370drv is the scanSTATION driver for the Perfection
// V37/V370, built on the lib-v370 submodule (own USB driver, no vendor
// software). Compiled in with the build tag v370.
package v370drv

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scanSTATION/lib-v370"
	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/output"
	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/replay"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/usb/usbfs"
	"github.com/scanSTATION/lib-v370/v370"
	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	"github.com/scanSTATION/scanSTATION-server/internal/postprocess"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// Name is the driver name (--driver v370, build tag v370).
const Name = "v370"

// Opener opens a USB transport to the scanner (see libv370.Opener).
type Opener = libv370.Opener

// UsbfsOpener opens the real scanner (sel = usbfs.OnlyReal) or the
// simulator attached via vhci-hcd (usbfs.OnlySimulated).
func UsbfsOpener(sel usbfs.Selector) Opener {
	if sel == usbfs.OnlySimulated {
		return libv370.SimulatedScanner()
	}
	return libv370.RealScanner()
}

// SimulatorOpener returns an opener that gives every session a fresh
// in-process simulator replaying the trace (tests and demos; no hardware).
func SimulatorOpener(tracePath string) (Opener, error) {
	tr, err := trace.Load(tracePath)
	if err != nil {
		return nil, err
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return func() (usb.Transport, string, error) {
		r, err := replay.New(tr, quiet)
		if err != nil {
			return nil, "", err
		}
		dev, err := sim.NewDevice(sim.DescriptorsFromTrace(tr), r, nil)
		if err != nil {
			return nil, "", err
		}
		return sim.NewTransport(dev), "simulated (" + filepath.Base(tracePath) + ")", nil
	}, nil
}

// Direct scans with our own driver (lib-v370). Each call opens its own
// session, so other programs can use the scanner in between.
type Direct struct {
	Open     Opener
	Firmware *firmware.Firmware // nil = ./firmware/esfwdd.bin (only needed after power-on)
	WorkDir  string             // where results are written before the caller moves them
	KeepRaw  bool               // also keep the raw stream
	Log      *slog.Logger
	// Now is passed to the session clock (tests pin it).
	Now func() time.Time
	// KeepOpen keeps the device open between calls (needed for button
	// watching; also saves the start-up per scan). The scanner is then
	// reserved for this program until Close. The kept device has no host
	// session while idle, so the Power button works (hold ~3 s).
	KeepOpen bool
	// VendorStart keeps the vendor app's carriage start position (scans
	// begin ≈ 1.2 mm after the glass corner); default: at the corner.
	VendorStart bool

	mu        sync.Mutex // one session at a time
	dev       *libv370.Device
	suspended int // >0: the scanner is lent to another program (the vendor fallback)
}

// errSuspended: the scanner is temporarily lent to another driver.
var errSuspended = fmt.Errorf("%w: scanner temporarily in use by another driver", driver.ErrScanner)

var (
	_ driver.Backend       = (*Direct)(nil)
	_ driver.ButtonWatcher = (*Direct)(nil)
	_ driver.Suspender     = (*Direct)(nil)
)

// Name implements Backend.
func (d *Direct) Name() string { return Name }

// openLocked returns the open device (opening it if needed). d.mu held.
func (d *Direct) openLocked(ctx context.Context) (*libv370.Device, error) {
	if d.dev != nil {
		return d.dev, nil
	}
	if d.suspended > 0 {
		return nil, errSuspended
	}
	// The kept device runs without a host session: the scanner's Power button
	// stays usable (a session blocks it) and buttons can still be read;
	// scans open a session only for their duration (libv370.Device.Scan).
	dev, err := libv370.OpenDevice(ctx, libv370.Options{Open: d.Open, Firmware: d.Firmware, Log: d.Log, Now: d.Now, NoSession: d.KeepOpen, VendorStart: d.VendorStart})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", driver.ErrScanner, err)
	}
	if d.KeepOpen {
		d.dev = dev
	}
	return dev, nil
}

// doneLocked closes dev unless it is the kept session and nothing failed.
func (d *Direct) doneLocked(dev *libv370.Device, failed bool) error {
	if d.KeepOpen && !failed && d.dev == dev {
		return nil
	}
	if d.dev == dev {
		d.dev = nil
	}
	return dev.Close()
}

func (d *Direct) session(ctx context.Context, fn func(dev *libv370.Device) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dev, err := d.openLocked(ctx)
	if err != nil {
		return err
	}
	ferr := fn(dev)
	// A hardware/protocol error may leave the session in an unknown state:
	// drop it, the next call reopens.
	cerr := d.doneLocked(dev, ferr != nil && errors.Is(ferr, driver.ErrScanner))
	if ferr != nil {
		return ferr
	}
	if cerr != nil {
		return fmt.Errorf("%w: %v", driver.ErrScanner, cerr)
	}
	return nil
}

// Suspend closes the kept session and keeps the scanner free for another
// program (another driver, e.g. the vendor app) until Resume.
func (d *Direct) Suspend() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.suspended++
	if d.dev != nil {
		if err := d.dev.Close(); err != nil && d.Log != nil {
			d.Log.Warn("closing scanner session", "err", err)
		}
		d.dev = nil
	}
}

// Resume undoes Suspend; the session is reopened on next use.
func (d *Direct) Resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.suspended > 0 {
		d.suspended--
	}
}

// Close ends the kept session (program shutdown).
func (d *Direct) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dev == nil {
		return nil
	}
	err := d.dev.Close()
	d.dev = nil
	return err
}

// WatchButtons polls the scanner's panel buttons every interval until ctx
// ends and calls onPress for every press (Printer, Email, PDF). It keeps a
// session open (sets KeepOpen). onPress runs in the watcher goroutine: a
// scan started from it blocks further polling, presses during the scan are
// reported by the scanner afterwards. If the scanner is off or unplugged
// the watcher retries with back-off and logs the problem once.
func (d *Direct) WatchButtons(ctx context.Context, interval time.Duration, onPress func(context.Context, driver.Button)) error {
	if interval < 50*time.Millisecond {
		return fmt.Errorf("button interval %s too short (min 50ms)", interval)
	}
	d.mu.Lock()
	d.KeepOpen = true
	d.mu.Unlock()
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	wait, lastErr := interval, ""
	for {
		var b v370.Button
		d.mu.Lock()
		dev, err := d.openLocked(ctx)
		if err == nil {
			if b, err = dev.ReadButton(ctx); err != nil {
				d.doneLocked(dev, true)
				err = fmt.Errorf("%w: %v", driver.ErrScanner, err)
			}
		}
		d.mu.Unlock()
		switch {
		case err != nil && errors.Is(err, errSuspended):
			wait = interval // lent to the vendor app; quietly try again
		case err != nil:
			if msg := err.Error(); msg != lastErr {
				log.Warn("button watcher: scanner not available, retrying", "err", err)
				lastErr = msg
			}
			wait = min(wait*2, 10*time.Second)
		default:
			if lastErr != "" {
				log.Info("button watcher: scanner available again")
				lastErr = ""
			}
			wait = interval
			if b != v370.ButtonNone {
				onPress(ctx, driver.Button(b.String()))
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// ListDevices implements Backend (opens a session, uploading the firmware
// after power-on).
func (d *Direct) ListDevices(ctx context.Context) ([]driver.Device, error) {
	var out []driver.Device
	err := d.session(ctx, func(dev *libv370.Device) error {
		id := dev.Identity()
		out = append(out, driver.Device{Name: "Perfection V37/V370 (" + id.Model + " " + id.Version + ", direct USB)", ModelID: "ES00DD"})
		return nil
	})
	return out, err
}

// Status implements Backend.
func (d *Direct) Status(ctx context.Context) (driver.Status, error) {
	var st driver.Status
	err := d.session(ctx, func(dev *libv370.Device) error {
		v, err := dev.Status(ctx)
		if err != nil {
			return fmt.Errorf("%w: %v", driver.ErrScanner, err)
		}
		msg := v.String()
		if v.Ready() {
			msg = "ready"
		}
		st = driver.Status{Ready: v.Ready(), Message: msg}
		return nil
	})
	return st, err
}

// profileKey identifies a direct profile by what a request asks for.
type profileKey struct {
	size      scan.DocumentSize
	dpi       int
	wmm, hmm  float64 // only for user-defined sizes
	colorMode scan.ColorMode
}

// directProfiles maps requests to built-in profiles. Only scans the scanner
// performs natively are listed: no host-side resampling or colour
// conversion is faked (the vendor scans 75/150 dpi at 100/200 and converts gray
// on the host).
var directProfiles = map[profileKey]string{
	{scan.SizeA4, 100, 0, 0, scan.ColorRGB}:           "a4-100dpi-color",
	{scan.SizeA4, 200, 0, 0, scan.ColorRGB}:           "a4-200dpi-color",
	{scan.SizeA4, 300, 0, 0, scan.ColorRGB}:           "a4-300dpi-color",
	{scan.SizeA4, 600, 0, 0, scan.ColorRGB}:           "a4-600dpi-color",
	{scan.SizeUserDef, 300, 100, 50, scan.ColorRGB}:   "user-100x50mm-300dpi",
	{scan.SizeUserDef, 600, 50, 50, scan.ColorRGB}:    "user-50x50mm-600dpi",
	{scan.SizeUserDef, 1200, 25, 25, scan.ColorRGB}:   "user-25x25mm-1200dpi",
	{scan.SizeUserDef, 1200, 148, 210, scan.ColorRGB}: "user-148x210mm-1200dpi",
}

// Supported describes the requests the driver covers.
func Supported() string {
	var s []string
	for k := range directProfiles {
		switch k.size {
		case scan.SizeA4:
			s = append(s, fmt.Sprintf("A4 colour %d dpi", k.dpi))
		default:
			s = append(s, fmt.Sprintf("%gx%g mm colour %d dpi", k.wmm, k.hmm, k.dpi))
		}
	}
	sort.Strings(s)
	return strings.Join(s, "; ")
}

// ProfileFor returns the built-in profile for a request, or driver.ErrUnsupported.
func ProfileFor(req scan.ScanRequest) (string, error) {
	k := profileKey{size: req.Size, dpi: req.Resolution, colorMode: req.ColorMode}
	if req.Size == scan.SizeUserDef {
		k.wmm, k.hmm = req.WidthMM, req.HeightMM
	}
	if name, ok := directProfiles[k]; ok {
		return name, nil
	}
	return "", fmt.Errorf("%w: the V370 driver supports %s", driver.ErrUnsupported, Supported())
}

func outputFormat(f scan.Format) (output.Format, error) {
	switch f {
	case scan.FormatPNG:
		return output.PNG, nil
	case scan.FormatJPEG:
		return output.JPEG, nil
	case scan.FormatPDF:
		return output.PDF, nil
	case scan.FormatRaw:
		return output.Raw, nil
	}
	return "", fmt.Errorf("%w: format %d", driver.ErrUnsupported, f)
}

// Scan implements Backend.
func (d *Direct) Scan(ctx context.Context, req scan.ScanRequest) (driver.Result, error) {
	if err := scan.Validate(req); err != nil {
		return driver.Result{}, err
	}
	name, err := ProfileFor(req)
	if err != nil {
		return driver.Result{}, err
	}
	format, err := outputFormat(req.Format)
	if err != nil {
		return driver.Result{}, err
	}
	work := d.WorkDir
	if work == "" {
		work = os.TempDir()
	}
	if err := os.MkdirAll(work, 0o700); err != nil {
		return driver.Result{}, err
	}

	var sc *libv370.Scan
	var identity string
	err = d.session(ctx, func(dev *libv370.Device) error {
		identity = dev.Identity().String()
		var err error
		if sc, err = dev.Scan(ctx, name); err != nil {
			return fmt.Errorf("%w: %v", driver.ErrScanner, err)
		}
		return nil
	})
	if err != nil {
		return driver.Result{}, err
	}
	opts := output.Options{Format: format, Quality: req.JpegQuality, KeepRaw: d.KeepRaw, PDFLossless: req.PDFLossless}
	var applied []string
	if !req.Post.Empty() && format != output.Raw {
		wmm, hmm, err := req.AreaMM()
		if err != nil {
			return driver.Result{}, err
		}
		opts.KeepOriginal = req.Post.KeepOriginal
		opts.Transform = func(img *image.RGBA, dpi int) (*image.RGBA, any, error) {
			// Image (0, 0) is the first scanned pixel at the glass corner
			// (lib-v370 docs/protocol.md, scan origin).
			return req.Post.Run(img, postprocess.Context{DPI: dpi, AreaMM: [2]float64{wmm, hmm}})
		}
		applied = req.Post.Names()
	}
	files, err := output.Write(filepath.Join(work, req.FileNamePrefix), sc.Profile, sc.Raw, opts,
		output.Meta{Identity: identity, Time: sc.Started.UTC(), Seconds: sc.Duration.Seconds()})
	if err != nil {
		return driver.Result{}, err
	}
	return driver.Result{Path: files[0], Extra: files[1:], Backend: Name, Postprocessed: applied}, nil
}
