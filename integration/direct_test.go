//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/scanSTATION/lib-v370/sim/synth"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A4 at 100 dpi from the direct driver: full scanned width, requested height.
const directW, directH = 848, 1168

func TestDirectCLIFormats(t *testing.T) {
	e := newCLIEnv(t)
	for _, f := range []string{"png", "jpeg", "pdf"} {
		t.Run(f, func(t *testing.T) {
			out, err := e.runDirect(t, "", "v370", "--dpi", "100", "--format", f, "-o", e.out, "--prefix", "d_"+f)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(out, "driver: v370") {
				t.Errorf("backend not reported:\n%s", out)
			}
			ext := map[string]string{"png": ".png", "jpeg": ".jpg", "pdf": ".pdf"}[f]
			b, err := os.ReadFile(filepath.Join(e.out, "d_"+f+ext))
			if err != nil {
				t.Fatalf("output missing: %v\n%s", err, out)
			}
			switch f {
			case "png":
				img, err := png.Decode(bytes.NewReader(b))
				if err != nil || img.Bounds().Dx() != directW || img.Bounds().Dy() != directH {
					t.Fatalf("png %v %v", img.Bounds(), err)
				}
			case "jpeg":
				cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
				if err != nil || cfg.Width != directW || cfg.Height != directH {
					t.Fatalf("jpeg %+v %v", cfg, err)
				}
			case "pdf":
				if n := pdfPages(t, b); n != 1 {
					t.Fatalf("pdf pages %d", n)
				}
			}
			var meta struct {
				File    string `json:"file"`
				Profile string `json:"profile"`
				DPI     int    `json:"dpi"`
			}
			mb, err := os.ReadFile(filepath.Join(e.out, "d_"+f+".json"))
			if err != nil || json.Unmarshal(mb, &meta) != nil || meta.Profile != "a4-100dpi-color" || meta.DPI != 100 || meta.File != "d_"+f+ext {
				t.Fatalf("sidecar %s: %v", mb, err)
			}
		})
	}
}

func TestDirectCLIListStatus(t *testing.T) {
	e := newCLIEnv(t)
	if out, err := e.runDirect(t, "", "v370", "--list"); err != nil || !strings.Contains(out, "direct USB") {
		t.Fatalf("--list: %v\n%s", err, out)
	}
	if out, err := e.runDirect(t, "", "v370", "--status"); err != nil || !strings.Contains(out, "Scanner status: ready") {
		t.Fatalf("--status: %v\n%s", err, out)
	}
}

func TestDirectCLIUnsupportedAndFallback(t *testing.T) {
	e := newCLIEnv(t)
	// direct only: 75 dpi is not a native resolution → clear error.
	out, err := e.runDirect(t, "", "v370", "--dpi", "75", "-o", e.out)
	if err == nil || !strings.Contains(out, "the V370 driver supports") {
		t.Fatalf("expected unsupported error:\n%s", out)
	}
	// auto: the same request falls back to the vendor app (the fake here).
	out, err = e.runDirect(t, "", "auto", "--dpi", "75", "-o", e.out, "--prefix", "fb")
	if err != nil || !strings.Contains(out, "driver: epsonscan2") {
		t.Fatalf("fallback: %v\n%s", err, out)
	}
	if s := lastScan(t, e.log); s.Resolution != 75 {
		t.Fatalf("fake got %+v", s)
	}
	// auto with a supported request stays direct.
	out, err = e.runDirect(t, "", "auto", "--dpi", "100", "--format", "png", "-o", e.out, "--prefix", "au")
	if err != nil || !strings.Contains(out, "driver: v370") {
		t.Fatalf("auto direct: %v\n%s", err, out)
	}
	// png is never handed to the vendor app.
	out, err = e.runDirect(t, "", "epsonscan2", "--format", "png", "-o", e.out)
	if err == nil || !strings.Contains(out, "png") {
		t.Fatalf("epsonscan2 png must fail:\n%s", out)
	}
}

func TestDirectCLIMultiPage(t *testing.T) {
	e := newCLIEnv(t)
	out, err := e.runDirect(t, "ne", "v370", "--multi", "--dpi", "100", "-o", e.out, "--prefix", "dm")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(e.out, "dm.pdf"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := pdfPages(t, b); n != 2 {
		t.Fatalf("pages %d", n)
	}
	if left, _ := filepath.Glob(filepath.Join(e.out, "dm_0*")); len(left) != 0 {
		t.Errorf("page files left: %v", left)
	}
}

func TestDirectAPI(t *testing.T) {
	// Default postprocessing: A4 at 100 dpi rounded down (826 px wide), cut
	// from the scan's corner; the 100 dpi scan has only 1168 of the 1169
	// rows, and nothing is added. Pixels unchanged.
	code, body := doJSONAt(t, directURL, "POST", "/api/scan", map[string]any{"dpi": 100, "format": "png", "prefix": "api_direct"})
	if code != http.StatusOK || body["backend"] != "v370" || body["filename"] != "api_direct.png" ||
		fmt.Sprint(body["postprocessing"]) != "[crop]" {
		t.Fatalf("scan = %d %v", code, body)
	}
	img, err := png.Decode(bytes.NewReader(downloadAt(t, directURL, body["download_url"].(string))))
	if err != nil || img.Bounds().Dx() != 826 || img.Bounds().Dy() != directH {
		t.Fatalf("png %v %v", img.Bounds(), err)
	}
	for _, p := range [][2]int{{0, 0}, {300, 500}, {825, directH - 1}} {
		r, g, b := synth.Pattern(p[0], p[1])
		if c := color.RGBAModel.Convert(img.At(p[0], p[1])).(color.RGBA); c.R != r || c.G != g || c.B != b {
			t.Fatalf("pixel %v = %v, want %d %d %d", p, c, r, g, b)
		}
	}
	// The JSON sidecar is stored next to the image and records the step.
	if mb := downloadAt(t, directURL, "/api/files/api_direct.json"); !bytes.Contains(mb, []byte(`"profile": "a4-100dpi-color"`)) ||
		!bytes.Contains(mb, []byte(`"step": "crop"`)) {
		t.Fatalf("sidecar: %s", mb)
	}
	// "none": exactly as scanned (full width).
	code, body = doJSONAt(t, directURL, "POST", "/api/scan", map[string]any{"dpi": 100, "format": "png", "prefix": "api_asscanned", "postprocessing": "none"})
	if code != http.StatusOK || fmt.Sprint(body["postprocessing"]) != "[]" {
		t.Fatalf("scan none = %d %v", code, body)
	}
	img, err = png.Decode(bytes.NewReader(downloadAt(t, directURL, body["download_url"].(string))))
	if err != nil || img.Bounds().Dx() != directW || img.Bounds().Dy() != directH {
		t.Fatalf("png none %v %v", img.Bounds(), err)
	}
	code, body = doJSONAt(t, directURL, "POST", "/api/scan", map[string]any{"dpi": 100, "postprocessing": "sharpen"})
	if code != http.StatusBadRequest {
		t.Fatalf("unknown postprocessing = %d %v", code, body)
	}
	// Unsupported in direct-only mode → 400 with the supported list.
	code, body = doJSONAt(t, directURL, "POST", "/api/scan", map[string]any{"dpi": 75})
	if code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["error"]), "the V370 driver supports") {
		t.Fatalf("unsupported = %d %v", code, body)
	}
	code, body = doJSONAt(t, directURL, "GET", "/api/status", nil)
	if code != http.StatusOK || body["ready"] != true {
		t.Fatalf("status = %d %v", code, body)
	}
	code, body = doJSONAt(t, directURL, "GET", "/api/devices", nil)
	if devs, _ := body["devices"].([]any); code != http.StatusOK || len(devs) != 1 {
		t.Fatalf("devices = %d %v", code, body)
	}
}

func TestDirectAPIMultiPage(t *testing.T) {
	code, body := doJSONAt(t, directURL, "POST", "/api/scan/multi", map[string]any{"dpi": 100, "prefix": "api_dmulti"})
	if code != http.StatusCreated || body["backend"] != "v370" {
		t.Fatalf("start = %d %v", code, body)
	}
	id := body["session_id"].(string)
	if code, body = doJSONAt(t, directURL, "POST", "/api/scan/multi/"+id+"/next", nil); code != http.StatusOK {
		t.Fatalf("next = %d %v", code, body)
	}
	code, body = doJSONAt(t, directURL, "POST", "/api/scan/multi/"+id+"/finish", nil)
	if code != http.StatusOK || body["pages"] != float64(2) {
		t.Fatalf("finish = %d %v", code, body)
	}
	if n := pdfPages(t, downloadAt(t, directURL, body["download_url"].(string))); n != 2 {
		t.Fatalf("pages %d", n)
	}
}
