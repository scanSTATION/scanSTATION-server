// Package config loads the user configuration (JSON): the server's
// postprocessing and what the scanner's panel buttons do.
//
// Default location: $XDG_CONFIG_HOME/scanstation/config.json (usually
// ~/.config/scanstation/config.json). A missing file is created with the
// defaults, so users have a file to edit.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/postprocess"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// Button names (match libv370's v370.Button String values).
const (
	ButtonPDF     = "pdf"
	ButtonEmail   = "email"
	ButtonPrinter = "printer"
)

// Actions.
const (
	ActionNone = "none"
	ActionScan = "scan"
)

// ButtonAction configures one button.
type ButtonAction struct {
	Action    string  `json:"action"`               // "scan" or "none"
	Format    string  `json:"format,omitempty"`     // pdf, png, jpeg
	PDFImage  string  `json:"pdf_image,omitempty"`  // lossless (default) or jpeg — for format pdf
	Quality   int     `json:"quality,omitempty"`    // JPEG quality (jpeg, or pdf with pdf_image jpeg)
	DPI       int     `json:"dpi,omitempty"`        // e.g. 300
	Size      string  `json:"size,omitempty"`       // a4, letter, max, user
	WidthMM   float64 `json:"width_mm,omitempty"`   // size user
	HeightMM  float64 `json:"height_mm,omitempty"`  // size user
	OutputDir string  `json:"output_dir,omitempty"` // "~" allowed
	Prefix    string  `json:"prefix,omitempty"`     // "{datetime}" → 20261107-143012
	// Postprocessing overrides the server's postprocessing for this button.
	Postprocessing *postprocess.Config `json:"postprocessing,omitempty"`
	// MultiPage: a long press starts a multi-page PDF (short presses add
	// pages, a long press finishes it). Missing: on for format pdf.
	MultiPage *bool `json:"multi_page,omitempty"`
	// LongPressS is the long-press threshold in seconds (default 1.0).
	LongPressS float64 `json:"long_press_s,omitempty"`
	// MultiTimeoutMin finishes an open multi-page document after this many
	// minutes without a press (default 10).
	MultiTimeoutMin float64 `json:"multi_timeout_min,omitempty"`
}

// Defaults of the multi-page settings.
const (
	DefaultLongPress    = time.Second
	DefaultMultiTimeout = 10 * time.Minute
)

// MultiPageEnabled reports whether a long press starts a multi-page PDF.
func (a ButtonAction) MultiPageEnabled() bool {
	if a.Format != "pdf" {
		return false
	}
	return a.MultiPage == nil || *a.MultiPage
}

// LongPress is the long-press threshold.
func (a ButtonAction) LongPress() time.Duration {
	if a.LongPressS == 0 {
		return DefaultLongPress
	}
	return time.Duration(a.LongPressS * float64(time.Second))
}

// MultiTimeout is the idle time after which an open document is finished.
func (a ButtonAction) MultiTimeout() time.Duration {
	if a.MultiTimeoutMin == 0 {
		return DefaultMultiTimeout
	}
	return time.Duration(a.MultiTimeoutMin * float64(time.Minute))
}

// Config is the whole file.
type Config struct {
	// Postprocessing applies to every server scan (REST API and buttons);
	// missing: crop to the requested size (postprocess.Default).
	Postprocessing *postprocess.Config     `json:"postprocessing,omitempty"`
	Buttons        map[string]ButtonAction `json:"buttons"`
}

// Pipeline returns the server's postprocessing.
func (c Config) Pipeline() (*postprocess.Pipeline, error) {
	if c.Postprocessing == nil {
		return postprocess.New(postprocess.Default())
	}
	return postprocess.New(*c.Postprocessing)
}

// ButtonPipeline returns the postprocessing for a button: its own, or the
// server's.
func (c Config) ButtonPipeline(button string) (*postprocess.Pipeline, error) {
	if a, ok := c.Buttons[button]; ok && a.Postprocessing != nil {
		return postprocess.New(*a.Postprocessing)
	}
	return c.Pipeline()
}

// Default returns the default configuration: PDF → lossless PDF, 300 dpi,
// A4, into ~/Documents; Email and Printer do nothing yet.
func Default() Config {
	pp := postprocess.Default()
	return Config{Postprocessing: &pp, Buttons: map[string]ButtonAction{
		ButtonPDF: {Action: ActionScan, Format: "pdf", PDFImage: "lossless", DPI: 300, Size: "a4",
			OutputDir: "~/Documents", Prefix: "scan_{datetime}"},
		ButtonEmail:   {Action: ActionNone},
		ButtonPrinter: {Action: ActionNone},
	}}
}

// DefaultPath is the default configuration file location.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.TempDir(), "scanstation-config")
	}
	return filepath.Join(dir, "scanstation", "config.json")
}

const maxFileSize = 1 << 20

// Load reads and validates the file; if it does not exist, it is created
// with Default().
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		c := Default()
		if err := Save(path, c); err != nil {
			return Config{}, fmt.Errorf("config: creating %s: %w", path, err)
		}
		return c, nil
	}
	if err != nil {
		return Config{}, err
	}
	if len(b) > maxFileSize {
		return Config{}, fmt.Errorf("config: %s is larger than %d bytes", path, maxFileSize)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return c, nil
}

// Save writes the configuration (0600, directory 0700).
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// Validate checks the postprocessing and every button entry.
func (c Config) Validate() error {
	if _, err := c.Pipeline(); err != nil {
		return err
	}
	for name, a := range c.Buttons {
		switch name {
		case ButtonPDF, ButtonEmail, ButtonPrinter:
		default:
			return fmt.Errorf("unknown button %q (use pdf, email, printer)", name)
		}
		if a.Action == ActionNone {
			continue
		}
		if a.Action != ActionScan {
			return fmt.Errorf("button %s: unknown action %q (use scan or none)", name, a.Action)
		}
		if _, err := a.Request(time.Now()); err != nil {
			return fmt.Errorf("button %s: %w", name, err)
		}
		if _, err := a.Dir(); err != nil {
			return fmt.Errorf("button %s: %w", name, err)
		}
		if _, err := c.ButtonPipeline(name); err != nil {
			return fmt.Errorf("button %s: %w", name, err)
		}
		if a.MultiPage != nil && *a.MultiPage && a.Format != "pdf" {
			return fmt.Errorf("button %s: multi_page needs format pdf", name)
		}
		if a.LongPressS != 0 && (a.LongPressS < 0.3 || a.LongPressS > 10) {
			return fmt.Errorf("button %s: long_press_s %.2f out of range (0.3–10)", name, a.LongPressS)
		}
		if a.MultiTimeoutMin != 0 && (a.MultiTimeoutMin < 0.05 || a.MultiTimeoutMin > 1440) {
			return fmt.Errorf("button %s: multi_timeout_min %.2f out of range (0.05–1440)", name, a.MultiTimeoutMin)
		}
	}
	return nil
}

// Dir returns the absolute output directory (with ~ expanded).
func (a ButtonAction) Dir() (string, error) {
	d := a.OutputDir
	if d == "" {
		d = "~/Documents"
	}
	if d == "~" || strings.HasPrefix(d, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(home, strings.TrimPrefix(d, "~"))
	}
	if !filepath.IsAbs(d) {
		return "", fmt.Errorf("output_dir %q must be absolute (or start with ~/)", a.OutputDir)
	}
	return filepath.Clean(d), nil
}

// Request builds the scan request for a press at time t.
func (a ButtonAction) Request(t time.Time) (scan.ScanRequest, error) {
	formats := map[string]scan.Format{"pdf": scan.FormatPDF, "png": scan.FormatPNG, "jpeg": scan.FormatJPEG, "jpg": scan.FormatJPEG}
	f, ok := formats[a.Format]
	if !ok {
		return scan.ScanRequest{}, fmt.Errorf("unknown format %q (use pdf, png, jpeg)", a.Format)
	}
	sizes := map[string]scan.DocumentSize{"a4": scan.SizeA4, "letter": scan.SizeLetter, "max": scan.SizeMaxArea, "user": scan.SizeUserDef}
	size, ok := sizes[a.Size]
	if !ok {
		return scan.ScanRequest{}, fmt.Errorf("unknown size %q (use a4, letter, max, user)", a.Size)
	}
	lossless := true
	switch a.PDFImage {
	case "", "lossless":
	case "jpeg":
		lossless = false
	default:
		return scan.ScanRequest{}, fmt.Errorf("unknown pdf_image %q (use lossless or jpeg)", a.PDFImage)
	}
	q := a.Quality
	if q == 0 {
		q = 85
	}
	prefix := a.Prefix
	if prefix == "" {
		prefix = "scan_{datetime}"
	}
	prefix = strings.ReplaceAll(prefix, "{datetime}", t.Format("20060102-150405"))
	req := scan.ScanRequest{
		Resolution: a.DPI, Format: f, ColorMode: scan.ColorRGB, Size: size,
		WidthMM: a.WidthMM, HeightMM: a.HeightMM,
		FileNamePrefix: prefix, JpegQuality: q, PDFLossless: lossless && f == scan.FormatPDF,
	}
	return req, scan.Validate(req)
}
