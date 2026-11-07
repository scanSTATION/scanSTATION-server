//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"net/http"
	"strings"
	"sync"
	"testing"

	fakescan "github.com/scanSTATION/lib-v370/vendorapp/fake"
)

func TestAPIHealth(t *testing.T) {
	code, body := doJSON(t, "GET", "/api/health", nil)
	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("health = %d %v", code, body)
	}
}

func TestAPIDevicesAndStatus(t *testing.T) {
	code, body := doJSON(t, "GET", "/api/devices", nil)
	if code != http.StatusOK {
		t.Fatalf("devices = %d %v", code, body)
	}
	devs, _ := body["devices"].([]any)
	if len(devs) != 1 {
		t.Fatalf("devices = %v", body)
	}
	d := devs[0].(map[string]any)
	if d["name"] != fakescan.DeviceName || d["model_id"] != fakescan.ModelID {
		t.Fatalf("device = %v", d)
	}
	code, body = doJSON(t, "GET", "/api/status", nil)
	if code != http.StatusOK || body["ready"] != true || body["status"] != "ready" {
		t.Fatalf("status = %d %v", code, body)
	}
}

func TestAPISingleScanJPEG(t *testing.T) {
	code, body := doJSON(t, "POST", "/api/scan", map[string]any{
		"dpi": 75, "format": "jpeg", "quality": 100, "prefix": "api_jpeg",
	})
	if code != http.StatusOK {
		t.Fatalf("scan = %d %v", code, body)
	}
	if body["filename"] != "api_jpeg.jpg" {
		t.Fatalf("filename = %v", body["filename"])
	}
	img := download(t, body["download_url"].(string))
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	// A4 at 75 DPI: 210 mm → 620 px, 297 mm → 877 px
	if cfg.Width != 620 || cfg.Height != 877 {
		t.Fatalf("image %dx%d, want 620x877", cfg.Width, cfg.Height)
	}
	s := lastScan(t, logFile)
	if s.Resolution != 75 || s.JpegQuality != 100 || s.ImageFormat != fakescan.FormatJPEG || s.ColorType != 0 {
		t.Fatalf("settings passed to the vendor app = %+v", s)
	}
}

func TestAPISingleScanVariants(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
		file   string
		check  func(t *testing.T, b []byte, s fakescan.Settings)
	}{
		{"gray pdf", map[string]any{"dpi": 100, "format": "pdf", "color": "gray", "prefix": "api_pdf"}, "api_pdf.pdf",
			func(t *testing.T, b []byte, s fakescan.Settings) {
				if n := pdfPages(t, b); n != 1 {
					t.Fatalf("pages = %d", n)
				}
				if s.ColorType != 1 || s.ImageFormat != fakescan.FormatPDF {
					t.Fatalf("settings = %+v", s)
				}
			}},
		{"letter", map[string]any{"dpi": 100, "size": "letter", "prefix": "api_letter"}, "api_letter.jpg",
			func(t *testing.T, b []byte, s fakescan.Settings) {
				cfg, _ := jpeg.DecodeConfig(bytes.NewReader(b))
				if cfg.Width != 850 || cfg.Height != 1100 {
					t.Fatalf("letter at 100 DPI = %dx%d, want 850x1100", cfg.Width, cfg.Height)
				}
			}},
		{"user size", map[string]any{"dpi": 100, "size": "user", "width_mm": 100, "height_mm": 50, "prefix": "api_user"}, "api_user.jpg",
			func(t *testing.T, b []byte, s fakescan.Settings) {
				if s.FixedDocumentSize != 0 {
					t.Fatalf("FixedDocumentSize = %d; a preset overrides the area in the vendor app", s.FixedDocumentSize)
				}
				cfg, _ := jpeg.DecodeConfig(bytes.NewReader(b))
				if cfg.Width != 394 || cfg.Height != 197 {
					t.Fatalf("100x50 mm at 100 DPI = %dx%d, want 394x197", cfg.Width, cfg.Height)
				}
			}},
		{"raw", map[string]any{"dpi": 50, "format": "raw", "prefix": "api_raw"}, "api_raw.raw",
			func(t *testing.T, b []byte, s fakescan.Settings) {
				if want := s.ScanAreaWidth * s.ScanAreaHeight * 3; len(b) != want {
					t.Fatalf("raw size %d, want %d", len(b), want)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := doJSON(t, "POST", "/api/scan", c.params)
			if code != http.StatusOK || body["filename"] != c.file {
				t.Fatalf("scan = %d %v", code, body)
			}
			b := download(t, body["download_url"].(string))
			c.check(t, b, lastScan(t, logFile))
		})
	}
}

func TestAPIRejectsBadRequests(t *testing.T) {
	cases := map[string]any{
		"unknown format": map[string]any{"format": "gif"},
		"unknown color":  map[string]any{"color": "sepia"},
		"unknown size":   map[string]any{"size": "a0"},
		"dpi too low":    map[string]any{"dpi": 10},
		"dpi too high":   map[string]any{"dpi": 99999},
		"quality":        map[string]any{"quality": 101},
		"user size":      map[string]any{"size": "user", "width_mm": 5000, "height_mm": 10},
		"user too wide":  map[string]any{"size": "user", "width_mm": 220, "height_mm": 50},
		"legal":          map[string]any{"size": "legal"},
		"traversal":      map[string]any{"prefix": "../../escape"},
		"glob prefix":    map[string]any{"prefix": "*"},
		"hidden prefix":  map[string]any{"prefix": ".hidden"},
		"unknown field":  `{"dpi":75,"evil":true}`,
		"malformed json": `{"dpi":`,
		"wrong type":     `{"dpi":"high"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			code, resp := doJSON(t, "POST", "/api/scan", body)
			if code != http.StatusBadRequest || resp["error"] == nil {
				t.Fatalf("got %d %v, want 400 with error", code, resp)
			}
		})
	}
	t.Run("multi traversal", func(t *testing.T) {
		code, _ := doJSON(t, "POST", "/api/scan/multi", map[string]any{"prefix": "../escape"})
		if code != http.StatusBadRequest {
			t.Fatalf("multi with traversal prefix = %d, want 400", code)
		}
	})
}

func TestAPIDownloadIsConfined(t *testing.T) {
	for _, p := range []string{"/api/files/..%2f..%2fetc%2fpasswd", "/api/files/does-not-exist.jpg"} {
		resp, err := http.Get(apiURL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("GET %s returned 200", p)
		}
	}
}

func TestAPIMultiPage(t *testing.T) {
	code, body := doJSON(t, "POST", "/api/scan/multi", map[string]any{"dpi": 75, "format": "jpeg", "prefix": "api_multi"})
	if code != http.StatusCreated {
		t.Fatalf("start = %d %v", code, body)
	}
	if w, _ := body["warnings"].([]any); len(w) != 1 {
		t.Errorf("expected a format warning, got %v", body["warnings"])
	}
	id := body["session_id"].(string)
	if s := lastScan(t, logFile); s.ImageFormat != fakescan.FormatPDF || s.FileNamePrefix != "api_multi_001" {
		t.Errorf("page 1 settings = %+v", s)
	}

	for page := 2; page <= 3; page++ {
		code, body = doJSON(t, "POST", "/api/scan/multi/"+id+"/next", nil)
		if code != http.StatusOK || body["page"] != float64(page) {
			t.Fatalf("next = %d %v", code, body)
		}
	}
	code, body = doJSON(t, "GET", "/api/scan/multi/"+id, nil)
	if code != http.StatusOK || body["pages"] != float64(3) || body["status"] != "waiting_for_input" {
		t.Fatalf("status = %d %v", code, body)
	}

	code, body = doJSON(t, "POST", "/api/scan/multi/"+id+"/finish", nil)
	if code != http.StatusOK || body["pages"] != float64(3) || body["filename"] != "api_multi.pdf" {
		t.Fatalf("finish = %d %v", code, body)
	}
	if n := pdfPages(t, download(t, body["download_url"].(string))); n != 3 {
		t.Fatalf("combined PDF has %d pages, want 3", n)
	}

	code, body = doJSON(t, "GET", "/api/scan/multi/"+id, nil)
	if code != http.StatusOK || body["status"] != "finished" {
		t.Fatalf("status after finish = %d %v", code, body)
	}
	if code, _ = doJSON(t, "POST", "/api/scan/multi/"+id+"/next", nil); code != http.StatusConflict {
		t.Fatalf("next after finish = %d, want 409", code)
	}
	if code, _ = doJSON(t, "POST", "/api/scan/multi/"+id+"/finish", nil); code != http.StatusConflict {
		t.Fatalf("finish twice = %d, want 409", code)
	}
}

func TestAPIMultiCancel(t *testing.T) {
	code, body := doJSON(t, "POST", "/api/scan/multi", map[string]any{"dpi": 50, "prefix": "api_cancel"})
	if code != http.StatusCreated {
		t.Fatalf("start = %d %v", code, body)
	}
	id := body["session_id"].(string)
	if code, body = doJSON(t, "DELETE", "/api/scan/multi/"+id, nil); code != http.StatusOK || body["status"] != "cancelled" {
		t.Fatalf("cancel = %d %v", code, body)
	}
	for _, req := range [][2]string{{"GET", ""}, {"POST", "/next"}, {"POST", "/finish"}, {"DELETE", ""}} {
		if code, _ := doJSON(t, req[0], "/api/scan/multi/"+id+req[1], nil); code != http.StatusNotFound {
			t.Errorf("%s after cancel = %d, want 404", req, code)
		}
	}
}

func TestAPIScannerFailures(t *testing.T) {
	t.Run("scan fails", func(t *testing.T) {
		setFakeControl(t, fakescan.Control{Fail: []string{"scan"}})
		code, body := doJSON(t, "POST", "/api/scan", map[string]any{"dpi": 50, "prefix": "api_fail"})
		if code != http.StatusInternalServerError || !strings.Contains(fmt.Sprint(body["error"]), "injected failure") {
			t.Fatalf("got %d %v", code, body)
		}
	})
	t.Run("no scanner", func(t *testing.T) {
		setFakeControl(t, fakescan.Control{NoDevices: true})
		code, body := doJSON(t, "GET", "/api/devices", nil)
		if devs, _ := body["devices"].([]any); code != http.StatusOK || len(devs) != 0 {
			t.Fatalf("devices = %d %v", code, body)
		}
		code, body = doJSON(t, "POST", "/api/scan", map[string]any{"dpi": 50, "prefix": "api_nodev"})
		if code != http.StatusInternalServerError || !strings.Contains(fmt.Sprint(body["error"]), "no scanner devices") {
			t.Fatalf("scan = %d %v", code, body)
		}
	})
	t.Run("status error", func(t *testing.T) {
		setFakeControl(t, fakescan.Control{Fail: []string{"status"}})
		code, body := doJSON(t, "GET", "/api/status", nil)
		if code != http.StatusServiceUnavailable || !strings.Contains(fmt.Sprint(body["error"]), "unexpected error") {
			t.Fatalf("status = %d %v, want 503", code, body)
		}
	})
	t.Run("status caution", func(t *testing.T) {
		setFakeControl(t, fakescan.Control{Caution: "Cover open"})
		code, body := doJSON(t, "GET", "/api/status", nil)
		if code != http.StatusOK || body["ready"] != false || body["status"] != "Cover open" {
			t.Fatalf("status = %d %v", code, body)
		}
	})
	t.Run("list fails", func(t *testing.T) {
		setFakeControl(t, fakescan.Control{Fail: []string{"list"}})
		if code, _ := doJSON(t, "GET", "/api/devices", nil); code != http.StatusInternalServerError {
			t.Fatalf("devices = %d, want 500", code)
		}
	})
	t.Run("multi page 2 fails", func(t *testing.T) {
		code, body := doJSON(t, "POST", "/api/scan/multi", map[string]any{"dpi": 50, "prefix": "api_mfail"})
		if code != http.StatusCreated {
			t.Fatalf("start = %d %v", code, body)
		}
		id := body["session_id"].(string)
		setFakeControl(t, fakescan.Control{Fail: []string{"scan"}})
		if code, _ := doJSON(t, "POST", "/api/scan/multi/"+id+"/next", nil); code != http.StatusInternalServerError {
			t.Fatalf("next = %d, want 500", code)
		}
		code, body = doJSON(t, "GET", "/api/scan/multi/"+id, nil)
		if body["status"] != "error" || body["error"] == "" {
			t.Fatalf("session after failure = %v", body)
		}
		doJSON(t, "DELETE", "/api/scan/multi/"+id, nil)
	})
}

// Concurrent requests are serialised on the scanner and all succeed.
func TestAPIConcurrentScans(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan string, 4)
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := doJSON(t, "POST", "/api/scan", map[string]any{"dpi": 50, "prefix": fmt.Sprintf("api_conc_%d", i)})
			if code != http.StatusOK || body["filename"] != fmt.Sprintf("api_conc_%d.jpg", i) {
				errs <- fmt.Sprintf("scan %d: %d %v", i, code, body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
