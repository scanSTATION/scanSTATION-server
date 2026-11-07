//go:build integration && hwsim

// Package hwsim is the Tier-2 integration test: the real vendor app talks
// over USB to the simulator (USB/IP + vhci-hcd) replaying a capture of the
// real V370. Needs private recordings (not part of the repository) and
// The vendor app. Run it via scripts/hwsim-test.sh, which starts and attaches the
// simulator and sets the environment:
//
//	USBSIM_TRACE        trace being replayed (its meta holds the capture scenario)
//	USBSIM_STATUS_URL   simulator status endpoint
//	SCANSTATION_BIN     scanstation binary (with the epsonscan2 driver)
//	VENDORAPP_NATIVE   "1" to use /opt/epsonscan2 instead of the Flatpak
//	HWSIM_STRICT        "1" to require a byte-identical output file
package hwsim

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/jpeg"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/sim/replay"
	"github.com/scanSTATION/lib-v370/sim/trace"
)

func env(t *testing.T, k string) string {
	t.Helper()
	v := os.Getenv(k)
	if v == "" {
		t.Skipf("%s not set — run via scripts/hwsim-test.sh", k)
	}
	return v
}

func simStats(t *testing.T, url string) replay.Stats {
	t.Helper()
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc struct {
		Replay replay.Stats `json:"replay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc.Replay
}

// scenarioArgs strips flags the test controls itself.
func scenarioArgs(meta trace.Meta) []string {
	var out []string
	skipNext := false
	for _, a := range meta.Scenario {
		if skipNext {
			skipNext = false
			continue
		}
		switch {
		case a == "--native", a == "--epsonscan2-native":
			continue
		case a == "-o", a == "--o", a == "--prefix", a == "-prefix", a == "--backend", a == "-backend", a == "--driver", a == "-driver":
			skipNext = true // the test sets these itself
			continue
		case strings.HasSuffix(a, "-scan"), strings.HasSuffix(a, "scanstation"): // captures record the command too
			continue
		}
		out = append(out, a)
	}
	return out
}

func TestReplayedScan(t *testing.T) {
	tracePath := env(t, "USBSIM_TRACE")
	statusURL := env(t, "USBSIM_STATUS_URL")
	bin := env(t, "SCANSTATION_BIN")

	tr, err := trace.Load(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	args := scenarioArgs(tr.Meta)
	if len(args) == 0 {
		t.Fatal("trace meta has no scenario; record it with scripts/capture-scan.sh")
	}
	before := simStats(t, statusURL)

	out := t.TempDir()
	full := append([]string{"--driver", "epsonscan2"}, args...)
	if os.Getenv("VENDORAPP_NATIVE") == "1" {
		full = append([]string{"--epsonscan2-native"}, full...)
	}
	full = append(full, "-o", out, "--prefix", "hwsim")
	cmd := exec.Command(bin, full...)
	t.Logf("running: %s %s", bin, strings.Join(full, " "))
	res, err := cmd.CombinedOutput()
	t.Logf("scanstation output:\n%s", res)
	after := simStats(t, statusURL)
	if after.Unmatched > before.Unmatched {
		for _, d := range after.Divergences {
			t.Logf("divergence: %+v", d)
		}
		t.Errorf("the vendor app sent %d command(s) that are not in the capture", after.Unmatched-before.Unmatched)
	}
	if err != nil {
		t.Fatalf("scan through simulator failed: %v", err)
	}
	if after.Commands == before.Commands {
		t.Fatal("simulator received no commands — is the real scanner used instead?")
	}

	files, _ := filepath.Glob(filepath.Join(out, "hwsim*"))
	if len(files) != 1 {
		t.Fatalf("expected one output file, got %v", files)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	switch strings.ToLower(filepath.Ext(files[0])) {
	case ".jpg", ".jpeg":
		if _, err := jpeg.DecodeConfig(bytes.NewReader(b)); err != nil {
			t.Fatalf("output is not a valid JPEG: %v", err)
		}
	case ".pdf":
		if !bytes.HasPrefix(b, []byte("%PDF-")) {
			t.Fatal("output is not a PDF")
		}
	}
	if want := tr.Meta.OutputSHA256; want != "" {
		sum := sha256.Sum256(b)
		got := hex.EncodeToString(sum[:])
		if got != want {
			msg := "output differs from the capture's reference file (sha256 " + got + " vs " + want + ")"
			if os.Getenv("HWSIM_STRICT") == "1" {
				t.Error(msg)
			} else {
				t.Log(msg + " — set HWSIM_STRICT=1 to enforce")
			}
		} else {
			t.Log("output is byte-identical to the real scanner's")
		}
	}
	t.Logf("replay stats: %+v", after)
}
