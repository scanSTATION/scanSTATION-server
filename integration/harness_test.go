//go:build integration

// Package integration contains Tier-1 end-to-end tests: the real
// scanstation CLI and scanstation-server binaries are built and driven from the
// outside (HTTP, command line, files), with the vendor app replaced by
// cmd/vendorapp-fake. No scanner, vendor binary or root is needed.
//
// Run locally:      go test -tags integration ./integration/
// Run in Docker:    docker compose -f docker-compose.test.yml run --rm tests
//
// Environment (all optional):
//
//	SCAN_API_URL       use an already running server instead of starting one
//	FAKE_CONTROL_FILE  fake's control file as seen by that server (shared volume)
//	FAKE_LOG_FILE      fake's invocation log as seen by that server
package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	fakescan "github.com/scanSTATION/lib-v370/vendorapp/fake"
)

// The v370 driver runs against lib-v370's synthetic scanner (just switched
// on: a dummy firmware is uploaded); no recordings, no vendor files.

var (
	directURL   string // REST API in direct mode (simulator)
	binDir      string // built binaries
	apiURL      string // REST API base URL
	controlFile string // fake control file used by the API server
	logFile     string // fake invocation log used by the API server
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "scanstation-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)

	binDir = filepath.Join(tmp, "bin")
	// Both drivers compiled in (as with DRIVERS="v370 epsonscan2").
	for name, pkg := range map[string]string{
		"scanstation":        "../cmd/scanstation",
		"scanstation-server": "../cmd/scanstation-server",
		"vendorapp-fake":     "github.com/scanSTATION/lib-v370/cmd/vendorapp-fake",
	} {
		cmd := name
		out, err := exec.Command("go", "build", "-tags", "v370 epsonscan2", "-o", filepath.Join(binDir, name), pkg).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "building %s: %v\n%s", cmd, err, out)
			return 1
		}
	}

	if u := os.Getenv("SCAN_API_URL"); u != "" {
		apiURL = strings.TrimRight(u, "/")
		controlFile = os.Getenv("FAKE_CONTROL_FILE")
		logFile = os.Getenv("FAKE_LOG_FILE")
	} else {
		stop, err := startServer(tmp)
		if err != nil {
			fmt.Fprintln(os.Stderr, "starting server:", err)
			return 1
		}
		defer stop()
	}
	if err := waitHealthy(apiURL, 30*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// A second server in direct mode against the in-process simulator
	// (always started locally; needs only the committed traces).
	stopDirect, err := startDirectServer(tmp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting direct server:", err)
		return 1
	}
	defer stopDirect()
	if err := waitHealthy(directURL, 30*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// startServer launches scanstation-server with the fake on a free port, in an
// isolated HOME (the vendor app writes to ~/Desktop, SF2 files go to ~/Documents).
func startServer(tmp string) (func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr := ln.Addr().String()
	ln.Close()

	home := filepath.Join(tmp, "server-home")
	controlFile = filepath.Join(tmp, "fake-control.json")
	logFile = filepath.Join(tmp, "fake-log.jsonl")
	cmd := exec.Command(filepath.Join(binDir, "scanstation-server"),
		"--addr", addr, "-o", filepath.Join(tmp, "server-out"), "--driver", "epsonscan2",
		"--epsonscan2-native", "--epsonscan2-native-path", filepath.Join(binDir, "vendorapp-fake"))
	cmd.Env = append(os.Environ(), "HOME="+home,
		"VENDORAPP_FAKE_CONTROL="+controlFile, "VENDORAPP_FAKE_LOG="+logFile)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	apiURL = "http://" + addr
	return func() { cmd.Process.Kill(); cmd.Wait() }, nil
}

// startDirectServer launches scanstation-server with --driver v370 and the
// simulator; the vendor fallback is not involved.
func startDirectServer(tmp string) (func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr := ln.Addr().String()
	ln.Close()
	cmd := exec.Command(filepath.Join(binDir, "scanstation-server"),
		"--addr", addr, "-o", filepath.Join(tmp, "direct-out"),
		"--driver", "v370", "--v370-simulate", "synthetic", "--buttons=false")
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(tmp, "direct-home"))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	directURL = "http://" + addr
	return func() { cmd.Process.Kill(); cmd.Wait() }, nil
}

func waitHealthy(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := http.Get(base + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server at %s not healthy after %s: %v", base, timeout, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// --- fake control / log ------------------------------------------------------

// setFakeControl writes the fake's control file for the duration of the test.
func setFakeControl(t *testing.T, c fakescan.Control) {
	t.Helper()
	if controlFile == "" {
		t.Skip("FAKE_CONTROL_FILE not set for external server")
	}
	b, _ := json.Marshal(c)
	if err := os.WriteFile(controlFile, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.WriteFile(controlFile, []byte("{}"), 0o644) })
}

// lastScan returns the settings of the most recent successful --scan logged
// by the fake.
func lastScan(t *testing.T, path string) fakescan.Settings {
	t.Helper()
	if path == "" {
		t.Skip("fake log not available")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var last *fakescan.Settings
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var inv fakescan.Invocation
		if json.Unmarshal(sc.Bytes(), &inv) == nil && inv.Settings != nil && inv.ExitCode == 0 {
			last = inv.Settings
		}
	}
	if last == nil {
		t.Fatal("no successful scan in fake log")
	}
	return *last
}

// --- HTTP helpers ------------------------------------------------------------

func doJSON(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	return doJSONAt(t, apiURL, method, path, body)
}

func doJSONAt(t *testing.T, base, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			j, _ := json.Marshal(b)
			rd = bytes.NewReader(j)
		}
	}
	req, _ := http.NewRequest(method, base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s %s: non-JSON response (%d): %s", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode, m
}

func download(t *testing.T, url string) []byte {
	t.Helper()
	return downloadAt(t, apiURL, url)
}

func downloadAt(t *testing.T, base, url string) []byte {
	t.Helper()
	resp, err := http.Get(base + url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, resp.StatusCode, b)
	}
	return b
}

// --- file checks -------------------------------------------------------------

var pageRe = regexp.MustCompile(`/Type\s*/Page[^s]`)

// pdfPages counts the pages of a PDF (pdfinfo if available, else a scan of
// the uncompressed object dictionaries our fake and pdfunite produce).
func pdfPages(t *testing.T, pdf []byte) int {
	t.Helper()
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("not a PDF (%d bytes)", len(pdf))
	}
	if _, err := exec.LookPath("pdfinfo"); err == nil {
		cmd := exec.Command("pdfinfo", "-")
		cmd.Stdin = bytes.NewReader(pdf)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("pdfinfo: %v\n%s", err, out)
		}
		var n int
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "Pages:") {
				fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(l, "Pages:")), "%d", &n)
			}
		}
		return n
	}
	return len(pageRe.FindAll(pdf, -1))
}

// --- CLI helpers -------------------------------------------------------------

type cliEnv struct {
	home    string
	out     string
	control string
	log     string
}

func newCLIEnv(t *testing.T) cliEnv {
	d := t.TempDir()
	return cliEnv{
		home: filepath.Join(d, "home"), out: filepath.Join(d, "out"),
		control: filepath.Join(d, "control.json"), log: filepath.Join(d, "fake.jsonl"),
	}
}

// runDirect runs scanstation with the given driver against the simulator
// (and the fake vendor app as fallback).
func (e cliEnv) runDirect(t *testing.T, stdin, backendName string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"--driver", backendName, "--v370-simulate", "synthetic",
		"--epsonscan2-native", "--epsonscan2-native-path", filepath.Join(binDir, "vendorapp-fake")}, args...)
	cmd := exec.Command(filepath.Join(binDir, "scanstation"), full...)
	cmd.Env = append(os.Environ(), "HOME="+e.home,
		"VENDORAPP_FAKE_CONTROL="+e.control, "VENDORAPP_FAKE_LOG="+e.log)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runCLI runs scanstation against the fake and returns combined output.
func (e cliEnv) run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"--driver", "epsonscan2", "--epsonscan2-native", "--epsonscan2-native-path", filepath.Join(binDir, "vendorapp-fake")}, args...)
	cmd := exec.Command(filepath.Join(binDir, "scanstation"), full...)
	cmd.Env = append(os.Environ(), "HOME="+e.home,
		"VENDORAPP_FAKE_CONTROL="+e.control, "VENDORAPP_FAKE_LOG="+e.log)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
