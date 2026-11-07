package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// Server is the REST API server for the V370 scanner.
type Server struct {
	backend   driver.Backend
	outputDir string
	sessions  *SessionStore
	scanMu    sync.Mutex // serializes scanner access (USB exclusivity)
	mux       *http.ServeMux
}

// NewServer creates a new API server that scans with b and stores results
// in outputDir.
func NewServer(b driver.Backend, outputDir string) *Server {
	s := &Server{
		backend:   b,
		outputDir: outputDir,
		sessions:  NewSessionStore(),
	}
	s.mux = s.buildRouter()
	return s
}

// Handler returns the http.Handler for the server (for use with http.Serve).
func (s *Server) Handler() http.Handler {
	return s.mux
}

// buildRouter registers all API routes using Go 1.22+ method-pattern routing.
func (s *Server) buildRouter() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.healthCheck)
	mux.HandleFunc("GET /api/devices", s.listDevices)
	mux.HandleFunc("GET /api/status", s.getStatus)

	mux.HandleFunc("POST /api/scan", s.singleScan)

	mux.HandleFunc("POST /api/scan/multi", s.multiStart)
	mux.HandleFunc("POST /api/scan/multi/{id}/next", s.multiNext)
	mux.HandleFunc("POST /api/scan/multi/{id}/finish", s.multiFinish)
	mux.HandleFunc("GET /api/scan/multi/{id}", s.multiStatus)
	mux.HandleFunc("DELETE /api/scan/multi/{id}", s.multiCancel)

	mux.HandleFunc("GET /api/files/{filename}", s.downloadFile)

	return mux
}

// --- helpers ---

// parseScanParams converts API ScanParams into a scan.ScanRequest.
// It validates string enums and returns an error on unknown values.
func parseScanParams(p ScanParams) (scan.ScanRequest, error) {
	fmtMap := map[string]scan.Format{
		"jpeg": scan.FormatJPEG,
		"jpg":  scan.FormatJPEG,
		"pdf":  scan.FormatPDF,
		"raw":  scan.FormatRaw,
		"png":  scan.FormatPNG,
	}
	imgFormat, ok := fmtMap[p.Format]
	if !ok {
		return scan.ScanRequest{}, fmt.Errorf("unknown format %q (use: jpeg, png, pdf, raw)", p.Format)
	}

	colorMap := map[string]scan.ColorMode{
		"rgb":     scan.ColorRGB,
		"color":   scan.ColorRGB,
		"gray":    scan.ColorGrayscale,
		"grey":    scan.ColorGrayscale,
		"lineart": scan.ColorLineArt,
		"bw":      scan.ColorLineArt,
	}
	colorMode, ok := colorMap[p.Color]
	if !ok {
		return scan.ScanRequest{}, fmt.Errorf("unknown color mode %q (use: rgb, gray, lineart)", p.Color)
	}

	sizeMap := map[string]scan.DocumentSize{
		"a4":     scan.SizeA4,
		"letter": scan.SizeLetter,
		"legal":  scan.SizeLegal,
		"max":    scan.SizeMaxArea,
		"user":   scan.SizeUserDef,
	}
	docSize, ok := sizeMap[p.Size]
	if !ok {
		return scan.ScanRequest{}, fmt.Errorf("unknown size %q (use: a4, letter, legal, max, user)", p.Size)
	}

	req := scan.ScanRequest{
		Resolution:     p.DPI,
		Format:         imgFormat,
		ColorMode:      colorMode,
		Size:           docSize,
		WidthMM:        p.WidthMM,
		HeightMM:       p.HeightMM,
		FileNamePrefix: p.Prefix,
		JpegQuality:    p.Quality,
	}
	if err := scan.Validate(req); err != nil {
		return scan.ScanRequest{}, err
	}
	return req, nil
}

// doScan performs one scan with the configured backend. The result files
// are in a temporary location; the caller moves or deletes them.
// The scanner mutex serialises scans (USB exclusivity).
func (s *Server) doScan(ctx context.Context, req scan.ScanRequest) (driver.Result, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	return s.backend.Scan(ctx, req)
}

// Scan performs a scan through the server's scan lock, so scans started
// elsewhere (e.g. by the scanner's buttons) are serialised with API scans.
func (s *Server) Scan(ctx context.Context, req scan.ScanRequest) (driver.Result, error) {
	return s.doScan(ctx, req)
}

// scanErrorStatus maps a scan error to an HTTP status: requests a backend
// cannot perform are client errors (400), everything else is a server/
// scanner error (500).
func scanErrorStatus(err error) int {
	if driver.IsUnsupported(err) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}

// downloadURL builds the relative URL for downloading a file by basename.
func downloadURL(filename string) string {
	return "/api/files/" + filename
}

// safeJoin joins the output directory with a filename, preventing path
// traversal by cleaning the path and ensuring the result stays within
// the output directory.
func safeJoin(baseDir, filename string) (string, error) {
	cleaned := filepath.Base(filename)
	if cleaned == "." || cleaned == "/" {
		return "", fmt.Errorf("invalid filename")
	}
	return filepath.Join(baseDir, cleaned), nil
}
