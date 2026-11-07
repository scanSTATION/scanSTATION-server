package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

func TestLoadCreatesDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Buttons[ButtonPDF].Action != ActionScan || c.Buttons[ButtonEmail].Action != ActionNone {
		t.Fatalf("defaults %+v", c)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v %v", fi, err)
	}
	// Reloading the written file gives the same result.
	c2, err := Load(p)
	if err != nil || c2.Buttons[ButtonPDF] != c.Buttons[ButtonPDF] {
		t.Fatalf("reload %+v %v", c2, err)
	}
}

func TestDefaultPDFRequest(t *testing.T) {
	a := Default().Buttons[ButtonPDF]
	at := time.Date(2026, 11, 7, 14, 30, 12, 0, time.UTC)
	req, err := a.Request(at)
	if err != nil {
		t.Fatal(err)
	}
	if req.Format != scan.FormatPDF || !req.PDFLossless || req.Resolution != 300 ||
		req.Size != scan.SizeA4 || req.FileNamePrefix != "scan_20261107-143012" {
		t.Fatalf("request %+v", req)
	}
	dir, err := a.Dir()
	home, _ := os.UserHomeDir()
	if err != nil || dir != filepath.Join(home, "Documents") {
		t.Fatalf("dir %q %v", dir, err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]string{
		"unknown button": `{"buttons":{"power":{"action":"scan"}}}`,
		"unknown action": `{"buttons":{"pdf":{"action":"print"}}}`,
		"bad format":     `{"buttons":{"pdf":{"action":"scan","format":"gif","dpi":300,"size":"a4"}}}`,
		"bad dpi":        `{"buttons":{"pdf":{"action":"scan","format":"pdf","dpi":10,"size":"a4"}}}`,
		"traversal":      `{"buttons":{"pdf":{"action":"scan","format":"pdf","dpi":300,"size":"a4","prefix":"../x"}}}`,
		"relative dir":   `{"buttons":{"pdf":{"action":"scan","format":"pdf","dpi":300,"size":"a4","output_dir":"docs"}}}`,
		"bad pdf_image":  `{"buttons":{"pdf":{"action":"scan","format":"pdf","pdf_image":"lzw","dpi":300,"size":"a4"}}}`,
		"unknown field":  `{"buttons":{"pdf":{"action":"scan","colour":"red"}}}`,
		"legal size":     `{"buttons":{"pdf":{"action":"scan","format":"pdf","dpi":300,"size":"legal"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "c.json")
			os.WriteFile(p, []byte(body), 0o600)
			if _, err := Load(p); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestJPEGInPDF(t *testing.T) {
	a := ButtonAction{Action: ActionScan, Format: "pdf", PDFImage: "jpeg", Quality: 70, DPI: 300, Size: "a4", OutputDir: "/srv/scans"}
	req, err := a.Request(time.Now())
	if err != nil || req.PDFLossless || req.JpegQuality != 70 || !strings.HasPrefix(req.FileNamePrefix, "scan_") {
		t.Fatalf("%+v %v", req, err)
	}
}
