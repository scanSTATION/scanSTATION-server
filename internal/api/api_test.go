package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// --- parseScanParams tests ---

func TestParseScanParamsValid(t *testing.T) {
	p := ScanParams{DPI: 300, Format: "jpeg", Color: "rgb", Size: "a4", Quality: 100, Prefix: "test"}
	req, err := parseScanParams(p)
	if err != nil {
		t.Fatalf("parseScanParams failed: %v", err)
	}
	if req.Resolution != 300 {
		t.Errorf("Resolution = %d, want 300", req.Resolution)
	}
	if req.Format != scan.FormatJPEG {
		t.Errorf("Format = %d, want %d", req.Format, scan.FormatJPEG)
	}
	if req.ColorMode != scan.ColorRGB {
		t.Errorf("ColorMode = %d, want %d", req.ColorMode, scan.ColorRGB)
	}
	if req.Size != scan.SizeA4 {
		t.Errorf("Size = %d, want %d", req.Size, scan.SizeA4)
	}
	if req.JpegQuality != 100 {
		t.Errorf("JpegQuality = %d, want 100", req.JpegQuality)
	}
	if req.FileNamePrefix != "test" {
		t.Errorf("FileNamePrefix = %q, want %q", req.FileNamePrefix, "test")
	}
}

func TestParseScanParamsInvalidFormat(t *testing.T) {
	p := ScanParams{Format: "bmp"}
	_, err := parseScanParams(p)
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestParseScanParamsInvalidColor(t *testing.T) {
	p := ScanParams{Format: "jpeg", Color: "cyan"}
	_, err := parseScanParams(p)
	if err == nil {
		t.Fatal("expected error for invalid color")
	}
}

func TestParseScanParamsInvalidSize(t *testing.T) {
	p := ScanParams{Format: "jpeg", Color: "rgb", Size: "b5"}
	_, err := parseScanParams(p)
	if err == nil {
		t.Fatal("expected error for invalid size")
	}
}

// --- ScanParams.ApplyDefaults tests ---

func TestApplyDefaults(t *testing.T) {
	p := ScanParams{}
	p.ApplyDefaults()
	if p.DPI != 300 {
		t.Errorf("DPI = %d, want 300", p.DPI)
	}
	if p.Format != "jpeg" {
		t.Errorf("Format = %q, want jpeg", p.Format)
	}
	if p.Quality != 85 {
		t.Errorf("Quality = %d, want 85", p.Quality)
	}
	if p.Prefix != "scan" {
		t.Errorf("Prefix = %q, want scan", p.Prefix)
	}
}

func TestApplyDefaultsPreservesExplicit(t *testing.T) {
	p := ScanParams{DPI: 600, Format: "pdf", Quality: 100, Prefix: "doc"}
	p.ApplyDefaults()
	if p.DPI != 600 {
		t.Errorf("DPI = %d, want 600", p.DPI)
	}
	if p.Format != "pdf" {
		t.Errorf("Format = %q, want pdf", p.Format)
	}
	if p.Quality != 100 {
		t.Errorf("Quality = %d, want 100", p.Quality)
	}
	if p.Prefix != "doc" {
		t.Errorf("Prefix = %q, want doc", p.Prefix)
	}
}

// --- SessionStore tests ---

func TestSessionStoreCreateGetDelete(t *testing.T) {
	store := NewSessionStore()
	params := DefaultScanParams()

	sess := store.Create(params)
	if sess.ID == "" {
		t.Fatal("session ID is empty")
	}
	if sess.Params.DPI != params.DPI {
		t.Errorf("session DPI = %d, want %d", sess.Params.DPI, params.DPI)
	}

	got, ok := store.Get(sess.ID)
	if !ok {
		t.Fatal("session not found after create")
	}
	if got.ID != sess.ID {
		t.Errorf("got ID = %q, want %q", got.ID, sess.ID)
	}

	store.Delete(sess.ID)
	_, ok = store.Get(sess.ID)
	if ok {
		t.Fatal("session still found after delete")
	}
}

func TestSessionStoreGetMissing(t *testing.T) {
	store := NewSessionStore()
	_, ok := store.Get("nonexistent")
	if ok {
		t.Fatal("expected not found for nonexistent ID")
	}
}

func TestGenerateSessionIDUnique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateSessionID()
		if ids[id] {
			t.Fatalf("duplicate session ID generated: %s", id)
		}
		ids[id] = true
		if len(id) != 16 {
			t.Errorf("ID length = %d, want 16", len(id))
		}
	}
}

// --- safeJoin tests ---

func TestSafeJoinValid(t *testing.T) {
	path, err := safeJoin("/output", "scan.jpg")
	if err != nil {
		t.Fatalf("safeJoin failed: %v", err)
	}
	if path != "/output/scan.jpg" {
		t.Errorf("path = %q, want /output/scan.jpg", path)
	}
}

func TestSafeJoinPreventsTraversal(t *testing.T) {
	// filepath.Base strips directory components, so "../../etc/passwd" becomes "passwd"
	path, err := safeJoin("/output", "../../etc/passwd")
	if err != nil {
		t.Fatalf("safeJoin failed: %v", err)
	}
	if strings.Contains(path, "..") {
		t.Errorf("path traversal not prevented: %s", path)
	}
}

// --- HTTP handler tests (no scanner needed) ---

// noScanner is a driver without a scanner: every scanner call fails.
type noScanner struct{}

func (noScanner) Name() string { return "none" }
func (noScanner) ListDevices(context.Context) ([]driver.Device, error) {
	return nil, driver.ErrScanner
}
func (noScanner) Status(context.Context) (driver.Status, error) {
	return driver.Status{}, driver.ErrScanner
}
func (noScanner) Scan(context.Context, scan.ScanRequest) (driver.Result, error) {
	return driver.Result{}, driver.ErrScanner
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	return NewServer(noScanner{}, dir), dir
}

func TestHealthCheck(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want %q", resp["status"], "ok")
	}
}

func TestSingleScanInvalidBody(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan", strings.NewReader("not json"))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSingleScanInvalidFormat(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `{"dpi":300,"format":"bmp","color":"rgb","size":"a4","quality":85,"prefix":"test"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan", strings.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestMultiStartMissingSession(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/scan/multi/nonexistent", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestMultiCancelMissingSession(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/scan/multi/nonexistent", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDownloadFileNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/files/nonexistent.jpg", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDownloadFileServesExisting(t *testing.T) {
	srv, dir := newTestServer(t)

	// Create a test file in the output directory.
	testContent := []byte("test file content")
	testFile := dir + "/test_scan.jpg"
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/files/test_scan.jpg", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "test file content" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "test file content")
	}
}

// --- Session lifecycle (no actual scan, just state) ---

func TestSessionLifecycle(t *testing.T) {
	store := NewSessionStore()
	params := DefaultScanParams()

	sess := store.Create(params)
	if sess.Status != StatusScanning {
		t.Errorf("initial status = %q, want %q", sess.Status, StatusScanning)
	}

	sess.Status = StatusWaiting
	if sess.Status != StatusWaiting {
		t.Errorf("status = %q, want %q", sess.Status, StatusWaiting)
	}

	sess.PagePDFs = append(sess.PagePDFs, "/tmp/page1.pdf")
	sess.PagePDFs = append(sess.PagePDFs, "/tmp/page2.pdf")
	if len(sess.PagePDFs) != 2 {
		t.Errorf("page count = %d, want 2", len(sess.PagePDFs))
	}

	sess.Status = StatusFinished
	sess.FinalFile = "/output/scan.pdf"
	if sess.Status != StatusFinished {
		t.Errorf("status = %q, want %q", sess.Status, StatusFinished)
	}

	store.Delete(sess.ID)
	_, ok := store.Get(sess.ID)
	if ok {
		t.Fatal("session still found after delete")
	}
}
