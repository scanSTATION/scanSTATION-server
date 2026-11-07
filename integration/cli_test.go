//go:build integration

package integration

import (
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	fakescan "github.com/scanSTATION/lib-v370/vendorapp/fake"
)

func TestCLIList(t *testing.T) {
	e := newCLIEnv(t)
	out, err := e.run(t, "", "--list")
	if err != nil || !strings.Contains(out, fakescan.DeviceName) || !strings.Contains(out, fakescan.ModelID) {
		t.Fatalf("--list: %v\n%s", err, out)
	}
	os.WriteFile(e.control, []byte(`{"no_devices":true}`), 0o644)
	if out, err := e.run(t, "", "--list"); err == nil || !strings.Contains(out, "No scanner devices found") {
		t.Fatalf("--list without scanner should fail: %v\n%s", err, out)
	}
}

func TestCLIStatus(t *testing.T) {
	e := newCLIEnv(t)
	if out, err := e.run(t, "", "--status"); err != nil || !strings.Contains(out, "Scanner status: ready") {
		t.Fatalf("--status: %v\n%s", err, out)
	}
	os.WriteFile(e.control, []byte(`{"caution":"Cover open"}`), 0o644)
	if out, err := e.run(t, "", "--status"); err == nil || !strings.Contains(out, "Scanner status: Cover open") {
		t.Fatalf("--status with caution must exit 1: %v\n%s", err, out)
	}
	os.WriteFile(e.control, []byte(`{"no_devices":true}`), 0o644)
	if out, err := e.run(t, "", "--status"); err == nil || !strings.Contains(out, "unexpected error") {
		t.Fatalf("--status without scanner must fail: %v\n%s", err, out)
	}
}

func TestCLISingleScan(t *testing.T) {
	e := newCLIEnv(t)
	out, err := e.run(t, "", "--dpi", "75", "--quality", "100", "--color", "gray", "-o", e.out, "--prefix", "cli")
	if err != nil {
		t.Fatalf("scan: %v\n%s", err, out)
	}
	f, err := os.Open(filepath.Join(e.out, "cli.jpg"))
	if err != nil {
		t.Fatalf("output missing: %v\n%s", err, out)
	}
	defer f.Close()
	if cfg, err := jpeg.DecodeConfig(f); err != nil || cfg.Width != 620 || cfg.Height != 877 {
		t.Fatalf("jpeg %+v %v", cfg, err)
	}
	if s := lastScan(t, e.log); s.JpegQuality != 100 || s.ColorType != 1 || s.Resolution != 75 {
		t.Fatalf("settings = %+v", s)
	}
	// The scan is moved away from ~/Desktop, and the SF2 temp file is removed.
	if m, _ := filepath.Glob(filepath.Join(e.home, "Desktop", "cli*")); len(m) != 0 {
		t.Errorf("leftover on Desktop: %v", m)
	}
	if m, _ := filepath.Glob(filepath.Join(e.home, "Documents", "*.SF2")); len(m) != 0 {
		t.Errorf("leftover settings files: %v", m)
	}
}

func TestCLIPDFAndTimestamp(t *testing.T) {
	e := newCLIEnv(t)
	out, err := e.run(t, "", "--dpi", "50", "--format", "pdf", "--timestamp", "-o", e.out)
	if err != nil {
		t.Fatalf("scan: %v\n%s", err, out)
	}
	m, _ := filepath.Glob(filepath.Join(e.out, "*.pdf"))
	if len(m) != 1 || !regexp.MustCompile(`/scan_\d{10,}\.pdf$`).MatchString(m[0]) {
		t.Fatalf("outputs = %v", m)
	}
	b, _ := os.ReadFile(m[0])
	if n := pdfPages(t, b); n != 1 {
		t.Fatalf("pages = %d", n)
	}
}

func TestCLIMultiPage(t *testing.T) {
	for _, keep := range []bool{false, true} {
		name := map[bool]string{false: "cleanup", true: "keep-pages"}[keep]
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			args := []string{"--multi", "--dpi", "50", "-o", e.out, "--prefix", "doc"}
			if keep {
				args = append(args, "--keep-pages")
			}
			// Non-interactive stdin: one byte per prompt. 'x' is invalid and re-prompts.
			out, err := e.run(t, "nxne", args...)
			if err != nil {
				t.Fatalf("multi: %v\n%s", err, out)
			}
			if !strings.Contains(out, "Invalid key") {
				t.Errorf("invalid key not reported:\n%s", out)
			}
			b, err := os.ReadFile(filepath.Join(e.out, "doc.pdf"))
			if err != nil {
				t.Fatalf("combined PDF missing:\n%s", out)
			}
			if n := pdfPages(t, b); n != 3 {
				t.Fatalf("pages = %d, want 3", n)
			}
			pages, _ := filepath.Glob(filepath.Join(e.out, "doc_0*.pdf"))
			if keep && len(pages) != 3 || !keep && len(pages) != 0 {
				t.Fatalf("page files = %v (keep=%v)", pages, keep)
			}
			if left, _ := filepath.Glob(filepath.Join(e.home, "Desktop", "doc_*")); len(left) != 0 {
				t.Errorf("leftover page PDFs on Desktop: %v", left)
			}
		})
	}
}

func TestCLIRejectsBadInput(t *testing.T) {
	cases := map[string][]string{
		"dpi":       {"--dpi", "10"},
		"quality":   {"--quality", "0"},
		"format":    {"--format", "gif"},
		"color":     {"--color", "sepia"},
		"size":      {"--size", "a0"},
		"traversal": {"--prefix", "../../escape"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			if out, err := e.run(t, "", append(args, "-o", e.out)...); err == nil {
				t.Fatalf("expected failure:\n%s", out)
			}
			if m, _ := filepath.Glob(filepath.Join(filepath.Dir(e.out), "*", "*")); len(m) > 0 {
				for _, f := range m {
					if strings.Contains(f, "escape") {
						t.Fatalf("file written outside output dir: %s", f)
					}
				}
			}
		})
	}
}

func TestCLIScanFailure(t *testing.T) {
	e := newCLIEnv(t)
	os.WriteFile(e.control, []byte(`{"fail":["scan"]}`), 0o644)
	out, err := e.run(t, "", "--dpi", "50", "-o", e.out)
	if err == nil || !strings.Contains(out, "injected failure") {
		t.Fatalf("expected scan failure to surface:\n%s", out)
	}
}
