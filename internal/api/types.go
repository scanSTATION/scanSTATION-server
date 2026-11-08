// Package api exposes the V370 scanner via a REST HTTP interface.
//
// The server provides endpoints for single-page scanning, multi-page scanning
// with session management, device listing, status queries, and file download.
// No external Go dependencies are used — routing relies on Go 1.22+ enhanced
// http.ServeMux patterns.
package api

import "github.com/scanSTATION/scanSTATION-server/internal/buttons"

// ScanParams holds the parameters for a scan request, accepting string
// values for format/color/size (as the CLI does) for ease of use from JSON.
type ScanParams struct {
	DPI      int     `json:"dpi"`
	Format   string  `json:"format"`              // jpeg, png, pdf, raw
	Color    string  `json:"color"`               // rgb, gray, lineart
	Size     string  `json:"size"`                // a4, letter, legal, max, user
	WidthMM  float64 `json:"width_mm,omitempty"`  // only when size=user
	HeightMM float64 `json:"height_mm,omitempty"` // only when size=user
	Quality  int     `json:"quality"`             // JPEG quality 1–100
	Prefix   string  `json:"prefix"`              // filename prefix
	// Postprocessing: "default" (the server's configuration) or "none"
	// (keep the scan as scanned).
	Postprocessing string `json:"postprocessing,omitempty"`
}

// DefaultScanParams returns sensible defaults matching the CLI defaults.
func DefaultScanParams() ScanParams {
	return ScanParams{
		DPI:     300,
		Format:  "jpeg",
		Color:   "rgb",
		Size:    "a4",
		Quality: 85,
		Prefix:  "scan",
	}
}

// ApplyDefaults fills in zero-value fields with defaults.
func (p *ScanParams) ApplyDefaults() {
	if p.DPI == 0 {
		p.DPI = 300
	}
	if p.Format == "" {
		p.Format = "jpeg"
	}
	if p.Color == "" {
		p.Color = "rgb"
	}
	if p.Size == "" {
		p.Size = "a4"
	}
	if p.Quality == 0 {
		p.Quality = 85
	}
	if p.Prefix == "" {
		p.Prefix = "scan"
	}
	if p.WidthMM == 0 {
		p.WidthMM = 210
	}
	if p.HeightMM == 0 {
		p.HeightMM = 297
	}
}

// --- Response types ---

// ScanResponse is returned by POST /api/scan.
type ScanResponse struct {
	File     string `json:"file"`              // full path on server
	Filename string `json:"filename"`          // basename for download
	Download string `json:"download_url"`      // relative URL for download
	Message  string `json:"message,omitempty"` // warnings or info
	Backend  string `json:"backend,omitempty"` // driver that scanned: v370 or epsonscan2
	// Postprocessing lists the steps applied (empty: as scanned).
	Postprocessing []string `json:"postprocessing"`
}

// MultiStartResponse is returned by POST /api/scan/multi.
type MultiStartResponse struct {
	SessionID string   `json:"session_id"`
	Page      int      `json:"page"`
	Status    string   `json:"status"`
	Warnings  []string `json:"warnings,omitempty"`
	Backend   string   `json:"backend,omitempty"`
}

// MultiNextResponse is returned by POST /api/scan/multi/{id}/next.
type MultiNextResponse struct {
	SessionID string `json:"session_id"`
	Page      int    `json:"page"`
	Status    string `json:"status"`
	Backend   string `json:"backend,omitempty"`
}

// MultiFinishResponse is returned by POST /api/scan/multi/{id}/finish.
type MultiFinishResponse struct {
	SessionID string `json:"session_id"`
	Pages     int    `json:"pages"`
	File      string `json:"file"`
	Filename  string `json:"filename"`
	Download  string `json:"download_url"`
}

// SessionStatusResponse is returned by GET /api/scan/multi/{id}.
type SessionStatusResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Pages     int    `json:"pages"`
	File      string `json:"file,omitempty"`
	Filename  string `json:"filename,omitempty"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

// StatusResponse is returned by GET /api/status.
type StatusResponse struct {
	Ready  bool   `json:"ready"`
	Status string `json:"status"` // "ready" or the scanner's caution message
	// ButtonDocuments lists multi-page documents being built with a button.
	ButtonDocuments []buttons.Document `json:"button_documents,omitempty"`
}

// DeviceListResponse is returned by GET /api/devices.
type DeviceListResponse struct {
	Devices []DeviceInfo `json:"devices"`
}

// DeviceInfo describes a scanner device.
type DeviceInfo struct {
	Name    string `json:"name"`
	ModelID string `json:"model_id"`
}

// ErrorResponse is returned for any error condition.
type ErrorResponse struct {
	Error string `json:"error"`
}

// SessionStatus represents the lifecycle state of a multi-page session.
type SessionStatus string

const (
	StatusWaiting   SessionStatus = "waiting_for_input"
	StatusScanning  SessionStatus = "scanning"
	StatusFinished  SessionStatus = "finished"
	StatusCancelled SessionStatus = "cancelled"
	StatusError     SessionStatus = "error"
)
