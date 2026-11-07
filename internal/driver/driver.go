// Package driver is the interface between scanSTATION and scanner drivers.
//
// A driver lives in its own package (internal/drivers/<name>), wraps a
// scanner library (a git submodule such as lib-v370) and registers itself in
// init(). Which drivers a binary contains is decided at build time with Go
// build tags (internal/drivers, Docker build argument DRIVERS), so an image
// contains only the libraries — and downloads only the firmware — it needs.
//
//	auto     = every compiled-in driver in priority order; a request goes to
//	           the first driver that supports it (fallback only on
//	           ErrUnsupported, never after a hardware error)
//	<name>   = one driver only
package driver

import (
	"context"
	"errors"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// Auto is the name selecting all compiled-in drivers.
const Auto = "auto"

// ErrUnsupported means the driver cannot perform this request at all (the
// request is valid, but e.g. the scanner has no such mode). Auto falls back
// on this error only.
var ErrUnsupported = errors.New("not supported by this driver")

// ErrScanner means the scanner (or its software) reported an error or is
// unreachable. The API maps it to 503.
var ErrScanner = errors.New("scanner error")

// IsUnsupported reports whether err means "not supported by this driver".
func IsUnsupported(err error) bool { return errors.Is(err, ErrUnsupported) }

// Device is a scanner reported by a driver.
type Device struct {
	Name    string
	ModelID string
}

// Status is the scanner state.
type Status struct {
	Ready   bool
	Message string
}

// Result of a scan: the image file and any companion files (e.g. the JSON
// sidecar), all in a temporary location; the caller moves or deletes them.
type Result struct {
	Path    string   // image file
	Extra   []string // companion files
	Backend string   // driver that actually scanned
	// Postprocessed lists the postprocessing steps applied (empty: the
	// file is as scanned, or the driver cannot postprocess).
	Postprocessed []string
}

// Files returns the image and all companion files.
func (r Result) Files() []string { return append([]string{r.Path}, r.Extra...) }

// Backend performs scans (one driver, or the auto chain).
type Backend interface {
	Name() string
	ListDevices(ctx context.Context) ([]Device, error)
	Status(ctx context.Context) (Status, error)
	// Scan performs one scan. req.FileNamePrefix names the output file.
	Scan(ctx context.Context, req scan.ScanRequest) (Result, error)
}

// Button is a scanner panel button, by name. The names are the keys of the
// button configuration ("pdf", "email", "printer", "power", …).
type Button string

// Buttons of the scanners supported so far.
const (
	ButtonPrinter Button = "printer"
	ButtonEmail   Button = "email"
	ButtonPDF     Button = "pdf"
	ButtonPower   Button = "power"
)

// ButtonWatcher is implemented by drivers that can watch panel buttons.
type ButtonWatcher interface {
	// WatchButtons polls until ctx ends and calls onPress for every press.
	WatchButtons(ctx context.Context, interval time.Duration, onPress func(context.Context, Button)) error
}

// Suspender is implemented by drivers that keep the scanner reserved; the
// auto chain suspends them while another driver (another program) uses the
// scanner.
type Suspender interface {
	Suspend()
	Resume()
}
