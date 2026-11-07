// Package vendorappdrv is the scanSTATION driver that scans through the vendor's
// own software, the vendor app (Flatpak or native install), using the
// vendorapp package of lib-v370. It covers what the direct driver cannot
// (gray/line-art, 75/150 dpi, Letter). Compiled in with the build tag
// The vendor app; the vendor app must be installed on the host.
package vendorappdrv

import (
	"context"
	"flag"
	"fmt"
	"os"

	es2 "github.com/scanSTATION/lib-v370/vendorapp"

	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// Name is the driver name (--driver epsonscan2, build tag epsonscan2).
const Name = "epsonscan2"

func init() {
	driver.Register(driver.Registration{
		Name:        Name,
		Description: "the vendor app (Flatpak or native), V370 fallback",
		Priority:    50,
		Flags:       registerFlags,
	})
}

func registerFlags(fs *flag.FlagSet) func(driver.Env) (driver.Backend, error) {
	native := fs.Bool("epsonscan2-native", false, "use a native vendor app binary instead of the Flatpak")
	path := fs.String("epsonscan2-native-path", "/opt/epsonscan2/bin/epsonscan2", "path to the native vendor app binary")
	network := fs.Bool("epsonscan2-allow-network", false, "allow the Flatpak app network access (disabled by default; the V370 is USB-only)")
	keep := fs.Bool("epsonscan2-keep-settings", false, "keep the generated .SF2 settings file (debugging)")
	return func(driver.Env) (driver.Backend, error) {
		cfg := es2.DefaultConfig()
		if *native {
			cfg.Mode = es2.ModeNative
			cfg.NativeBinaryPath = *path
		}
		if *network {
			cfg.NoNetwork = false
		}
		return &VendorApp{Cfg: cfg, KeepSettings: *keep}, nil
	}
}

// The vendor scans through the vendor app CLI.
type VendorApp struct {
	Cfg es2.Config
	// KeepSettings keeps the generated .SF2 file (debugging).
	KeepSettings bool
}

var _ driver.Backend = (*VendorApp)(nil)

// Name implements driver.Backend.
func (e *VendorApp) Name() string { return Name }

// ListDevices implements driver.Backend.
func (e *VendorApp) ListDevices(ctx context.Context) ([]driver.Device, error) {
	devs, err := es2.ListDevices(e.Cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", driver.ErrScanner, err)
	}
	out := make([]driver.Device, len(devs))
	for i, d := range devs {
		out[i] = driver.Device{Name: d.Name, ModelID: d.ModelID}
	}
	return out, nil
}

// Status implements driver.Backend.
func (e *VendorApp) Status(ctx context.Context) (driver.Status, error) {
	st, err := es2.GetStatus(e.Cfg)
	if err != nil {
		return driver.Status{}, fmt.Errorf("%w: %v", driver.ErrScanner, err)
	}
	return driver.Status{Ready: st.Ready, Message: st.Message}, nil
}

// settings translates a request; the numeric values of format, colour mode
// and size are shared with es2 on purpose.
func settings(req scan.ScanRequest) (es2.Settings, error) {
	if req.Format == scan.FormatPNG {
		return es2.Settings{}, fmt.Errorf("%w: png (the vendor app cannot write PNG)", driver.ErrUnsupported)
	}
	s := es2.Default()
	s.Resolution = req.Resolution
	s.Format = es2.Format(req.Format)
	s.ColorMode = es2.ColorMode(req.ColorMode)
	s.Size = es2.DocumentSize(req.Size)
	s.WidthMM, s.HeightMM = req.WidthMM, req.HeightMM
	s.FileNamePrefix = req.FileNamePrefix
	s.JpegQuality = req.JpegQuality
	s.OutputDir, s.OutputPath = 2, "" // Desktop: where the Flatpak may write
	if err := es2.Validate(s); err != nil {
		return es2.Settings{}, fmt.Errorf("%w: %v", driver.ErrUnsupported, err)
	}
	return s, nil
}

// Scan implements driver.Backend: build the .SF2, run the vendor app, find the
// output on ~/Desktop.
func (e *VendorApp) Scan(ctx context.Context, req scan.ScanRequest) (driver.Result, error) {
	s, err := settings(req)
	if err != nil {
		return driver.Result{}, err
	}
	content, err := es2.Build(s)
	if err != nil {
		return driver.Result{}, err
	}
	path, err := es2.WriteSettingsFile(content)
	if err != nil {
		return driver.Result{}, fmt.Errorf("writing settings file: %w", err)
	}
	if !e.KeepSettings {
		defer os.Remove(path)
	}
	out, err := es2.Scan(e.Cfg, es2.ScanOptions{SettingsFile: path})
	if err != nil {
		return driver.Result{}, fmt.Errorf("%w: %v", driver.ErrScanner, err)
	}
	desktop, err := es2.DesktopDir()
	if err != nil {
		return driver.Result{}, err
	}
	file, err := es2.FindScannedFile(desktop, req.FileNamePrefix)
	if err != nil {
		return driver.Result{}, fmt.Errorf("scan finished but no output file: %w (the vendor app said: %s)", err, out)
	}
	return driver.Result{Path: file, Backend: Name}, nil
}
