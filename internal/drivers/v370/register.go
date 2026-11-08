package v370drv

import (
	"flag"
	"fmt"

	"github.com/scanSTATION/lib-v370"
	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/sim/synth"
	"github.com/scanSTATION/lib-v370/usb/usbfs"

	"github.com/scanSTATION/scanSTATION-server/internal/driver"
)

// Synthetic selects the synthetic scanner for --v370-simulate.
const Synthetic = "synthetic"

func init() {
	driver.Register(driver.Registration{
		Name:        Name,
		Description: "Perfection V37/V370, own USB driver (lib-v370)",
		Priority:    10,
		Flags:       registerFlags,
	})
}

type flags struct {
	firmware     string
	allowUnknown bool
	keepRaw      bool
	simulate     string
	simButtons   string
	vendorStart  bool
	stock        bool
	slowBlink    bool
}

func registerFlags(fs *flag.FlagSet) func(driver.Env) (driver.Backend, error) {
	f := &flags{}
	fs.StringVar(&f.firmware, "v370-firmware", "", "firmware file (default: ./firmware/esfwdd.bin; needed after the scanner was switched on; lib-v370/scanstation/firmware.sh fetches it)")
	fs.BoolVar(&f.allowUnknown, "v370-allow-unknown-firmware", false, "accept a firmware file with an unknown SHA-256")
	fs.BoolVar(&f.keepRaw, "v370-keep-raw", false, "also keep the raw scanner stream (.raw)")
	fs.BoolVar(&f.vendorStart, "v370-vendor-start", false, "start scans where the vendor app does (≈ 1.2 mm after the glass corner) instead of at the corner")
	fs.BoolVar(&f.slowBlink, "v370-no-transfer-blink", false, "keep the normal busy blink while the image is transferred (with lib-v370's LED patch it blinks 4 times faster)")
	fs.BoolVar(&f.stock, "v370-stock-firmware", false, "upload the firmware without lib-v370's LED patch (no LED off state; buttons pause while the LED is orange)")
	fs.StringVar(&f.simulate, "v370-simulate", "", "testing only: \"synthetic\" (simulated scanner, no hardware) or a recorded trace file to replay")
	fs.StringVar(&f.simButtons, "v370-simulate-buttons", "", "testing only: comma-separated button presses the simulator reports (pdf,email,printer; pdf:2s = held 2 s)")
	return f.build
}

func (f *flags) build(env driver.Env) (driver.Backend, error) {
	d := &Direct{Open: UsbfsOpener(usbfs.OnlyReal), WorkDir: env.WorkDir, KeepRaw: f.keepRaw, Log: env.Log, VendorStart: f.vendorStart, StockFirmware: f.stock, NoTransferBlink: f.slowBlink}
	if f.firmware != "" {
		fw, err := firmware.Load(f.firmware, f.allowUnknown)
		if err != nil {
			return nil, err
		}
		d.Firmware = fw
	}
	var dev *synth.Device
	switch f.simulate {
	case "":
	case Synthetic:
		var err error
		if dev, err = synth.New(synth.Options{LEDPatch: !f.stock}); err != nil {
			return nil, err
		}
		d.Open = libv370.SyntheticScanner(dev)
		if d.Firmware == nil {
			d.Firmware = synth.DummyFirmware()
		}
	default:
		op, err := SimulatorOpener(f.simulate)
		if err != nil {
			return nil, err
		}
		d.Open = op
	}
	if f.simButtons != "" {
		if f.simulate == "" {
			return nil, fmt.Errorf("--v370-simulate-buttons needs --v370-simulate")
		}
		inj, err := ParseButtons(f.simButtons)
		if err != nil {
			return nil, err
		}
		if dev != nil {
			inj.Into(dev)
		} else {
			d.Open = inj.Wrap(d.Open)
		}
	}
	return d, nil
}
