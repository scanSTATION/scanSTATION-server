//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// writeButtonConfig creates a config: PDF → lossless PDF at 100 dpi (the
// simulated trace's resolution) into out; Email/Printer → none.
func writeButtonConfig(t *testing.T, out string) string {
	t.Helper()
	cfg := map[string]any{"buttons": map[string]any{
		"pdf":     map[string]any{"action": "scan", "format": "pdf", "pdf_image": "lossless", "dpi": 100, "size": "a4", "output_dir": out, "prefix": "btn_{datetime}"},
		"email":   map[string]any{"action": "none"},
		"printer": map[string]any{"action": "none"},
	}}
	b, _ := json.Marshal(cfg)
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// waitForPDF waits until a btn_*.pdf appears in dir.
func waitForPDF(t *testing.T, dir string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var m []string
		all, _ := filepath.Glob(filepath.Join(dir, "btn_*.pdf"))
		for _, f := range all {
			if !strings.HasSuffix(f, "-original.pdf") {
				m = append(m, f)
			}
		}
		if len(m) == 1 {
			if _, err := os.Stat(strings.TrimSuffix(m[0], ".pdf") + ".json"); err == nil {
				return m[0]
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no button scan appeared in %s", dir)
	return ""
}

func checkLosslessPDF(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("/FlateDecode")) || bytes.Contains(b, []byte("/DCTDecode")) {
		t.Fatal("PDF image is not lossless")
	}
	if n := pdfPages(t, b); n != 1 {
		t.Fatalf("pages %d", n)
	}
}

// scanstation --watch: Email (no action) then PDF → lossless PDF in the
// configured directory; Ctrl-C ends it cleanly.
func TestWatchCLIButtons(t *testing.T) {
	e := newCLIEnv(t)
	out := t.TempDir()
	cfg := writeButtonConfig(t, out)
	cmd := exec.Command(filepath.Join(binDir, "scanstation"), "--watch", "--driver", "v370",
		"--v370-simulate", "synthetic",
		"--v370-simulate-buttons", "email,pdf", "--config", cfg, "--button-interval", "100ms")
	cmd.Env = append(os.Environ(), "HOME="+e.home)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pdf := waitForPDF(t, out, 60*time.Second)
	cmd.Process.Signal(syscall.SIGINT)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit: %v\n%s", err, log.String())
	}
	checkLosslessPDF(t, pdf)
	for _, want := range []string{"Watching scanner buttons", "no action configured", "button=email", "button scan stored", "Stopped."} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}

// The server watches the buttons by default; a PDF press scans while the
// API keeps working on the same scanner session.
func TestServerButtons(t *testing.T) {
	out := t.TempDir()
	cfg := writeButtonConfig(t, out)
	// The PDF button keeps the original next to the cropped scan.
	var c map[string]any
	b, _ := os.ReadFile(cfg)
	json.Unmarshal(b, &c)
	c["buttons"].(map[string]any)["pdf"].(map[string]any)["postprocessing"] = map[string]any{
		"keep_original": true, "steps": []any{map[string]any{"step": "crop"}}}
	b, _ = json.Marshal(c)
	os.WriteFile(cfg, b, 0o600)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	cmd := exec.Command(filepath.Join(binDir, "scanstation-server"), "--addr", addr, "-o", t.TempDir(),
		"--driver", "v370", "--v370-simulate", "synthetic",
		"--v370-simulate-buttons", "pdf", "--config", cfg, "--button-interval", "100ms")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	}()
	base := "http://" + addr
	if err := waitHealthy(base, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	pdf := waitForPDF(t, out, 60*time.Second)
	checkLosslessPDF(t, pdf)
	// Cropped to A4 at 100 dpi; the original is kept.
	side, err := os.ReadFile(strings.TrimSuffix(pdf, ".pdf") + ".json")
	if err != nil || !bytes.Contains(side, []byte(`"width_px": 826`)) || !bytes.Contains(side, []byte(`"step": "crop"`)) ||
		!bytes.Contains(side, []byte(`-original.pdf"`)) {
		t.Fatalf("sidecar %s %v", side, err)
	}
	if _, err := os.Stat(strings.TrimSuffix(pdf, ".pdf") + "-original.pdf"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(base + "/api/status")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status after button scan: %v %v\n%s", resp, err, log.String())
	}
	resp.Body.Close()
	if !strings.Contains(log.String(), "Watching scanner buttons") || !strings.Contains(log.String(), "Postprocessing: crop") {
		t.Errorf("server log:\n%s", log.String())
	}
}

// Long press, short press, Power on the server: one PDF with two pages.
func TestServerButtonsMultiPage(t *testing.T) {
	out := t.TempDir()
	cfg := writeButtonConfig(t, out)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	cmd := exec.Command(filepath.Join(binDir, "scanstation-server"), "--addr", addr, "-o", t.TempDir(),
		"--driver", "v370", "--v370-simulate", "synthetic",
		"--v370-simulate-buttons", "pdf:2s,pdf,power", "--config", cfg, "--button-interval", "100ms")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	}()
	if err := waitHealthy("http://"+addr, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	pdf := waitForPDF(t, out, 90*time.Second)
	b, err := os.ReadFile(pdf)
	if err != nil || pdfPages(t, b) != 2 {
		t.Fatalf("document %s: %v\n%s", pdf, err, log.String())
	}
	side, err := os.ReadFile(strings.TrimSuffix(pdf, ".pdf") + ".json")
	if err != nil || !bytes.Contains(side, []byte(`"pages": 2`)) {
		t.Fatalf("sidecar %s %v", side, err)
	}
	for _, want := range []string{"multi-page document started", "multi-page document: page scanned", "multi-page document stored"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}
