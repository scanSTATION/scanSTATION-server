// Package scan defines a scan request independent of the scanner driver.
// Drivers translate it into their own settings and reject what their
// scanner cannot do (driver.ErrUnsupported), e.g. an area larger than its
// glass.
package scan

import (
	"fmt"
	"github.com/scanSTATION/scanSTATION-server/internal/postprocess"
	"strings"
	"unicode"
)

// Format is the output file format.
type Format int

const (
	FormatJPEG Format = 1   // JPEG (default, portable)
	FormatPDF  Format = 6   // PDF
	FormatRaw  Format = 0   // raw scanner data
	FormatPNG  Format = 100 // lossless PNG
)

// ColorMode controls the colour depth of the scan.
type ColorMode int

const (
	ColorRGB       ColorMode = 0 // 24-bit colour (default)
	ColorGrayscale ColorMode = 1 // 8-bit grayscale
	ColorLineArt   ColorMode = 2 // 1-bit black & white / line art
)

// DocumentSize selects the scan area.
type DocumentSize int

const (
	SizeA4      DocumentSize = iota // 210 × 297 mm
	SizeLetter                      // 8.5 × 11 in (215.9 × 279.4 mm)
	SizeLegal                       // 8.5 × 14 in
	SizeMaxArea                     // the whole glass of the scanner (driver-defined)
	SizeUserDef                     // WidthMM × HeightMM
)

// MaxAreaMM bounds user-defined areas before any driver sees them (A3+);
// drivers check their own glass.
const MaxAreaMM = 500.0

// ScanRequest holds the parameters for a single scan job.
type ScanRequest struct {
	Resolution int          // DPI
	Format     Format       // output image format
	ColorMode  ColorMode    // colour depth
	Size       DocumentSize // paper size
	// WidthMM and HeightMM are used only when Size == SizeUserDef.
	WidthMM  float64
	HeightMM float64
	// FileNamePrefix is the prefix for the output filename.
	FileNamePrefix string
	// JpegQuality controls the JPEG compression quality (1–100).
	JpegQuality int
	// PDFLossless asks for a lossless page image in PDF output (drivers
	// that cannot do it embed JPEG).
	PDFLossless bool
	// Post is the postprocessing to apply before the image is stored; nil
	// or empty keeps the scan as scanned. Drivers that deliver finished
	// files (already cropped by other software) report it as not applied.
	Post *postprocess.Pipeline
}

// Default returns a ScanRequest with sensible defaults: 300 DPI, A4, JPEG, RGB.
func Default() ScanRequest {
	return ScanRequest{
		Resolution:     300,
		Format:         FormatJPEG,
		ColorMode:      ColorRGB,
		Size:           SizeA4,
		FileNamePrefix: "scan",
		JpegQuality:    85,
	}
}

// AreaMM returns the requested area in millimetres; for SizeMaxArea it
// returns 0, 0 (the driver knows its glass).
func (r ScanRequest) AreaMM() (width, height float64, err error) {
	switch r.Size {
	case SizeA4:
		return 210, 297, nil
	case SizeLetter:
		return 215.9, 279.4, nil
	case SizeLegal:
		return 215.9, 355.6, nil
	case SizeMaxArea:
		return 0, 0, nil
	case SizeUserDef:
		return r.WidthMM, r.HeightMM, nil
	default:
		return 0, 0, fmt.Errorf("unknown document size %d", r.Size)
	}
}

// MaxPrefixLen bounds the filename prefix.
const MaxPrefixLen = 128

// ValidatePrefix checks a filename prefix. The prefix becomes part of file
// paths (driver output, glob patterns, the combined multi-page PDF), so
// path separators, glob metacharacters, control characters and a leading
// dot (hidden files, "..") are rejected.
func ValidatePrefix(p string) error {
	if p == "" {
		return fmt.Errorf("filename prefix is empty")
	}
	if len(p) > MaxPrefixLen {
		return fmt.Errorf("filename prefix longer than %d bytes", MaxPrefixLen)
	}
	if strings.HasPrefix(p, ".") {
		return fmt.Errorf("filename prefix %q must not start with '.'", p)
	}
	if strings.ContainsAny(p, `/\*?[]`) {
		return fmt.Errorf("filename prefix %q contains a path separator or glob character", p)
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return fmt.Errorf("filename prefix contains a control character")
		}
	}
	return nil
}

// Validate checks what every driver relies on: sane resolution, quality,
// area and prefix. Device limits are checked by the driver.
func Validate(r ScanRequest) error {
	if r.Resolution < 50 || r.Resolution > 12800 {
		return fmt.Errorf("resolution %d out of range (50–12800)", r.Resolution)
	}
	if r.JpegQuality < 1 || r.JpegQuality > 100 {
		return fmt.Errorf("jpeg quality %d out of range (1–100)", r.JpegQuality)
	}
	w, h, err := r.AreaMM()
	if err != nil {
		return err
	}
	if r.Size != SizeMaxArea && !(w > 0 && h > 0 && w <= MaxAreaMM && h <= MaxAreaMM) {
		return fmt.Errorf("scan area %.1f × %.1f mm out of range", w, h)
	}
	switch r.Format {
	case FormatJPEG, FormatPDF, FormatRaw, FormatPNG:
	default:
		return fmt.Errorf("unknown format %d", r.Format)
	}
	return ValidatePrefix(r.FileNamePrefix)
}
