package v370drv

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/v370"
)

// ButtonInjector wraps simulated scanners so that `1e 85` (button query)
// answers with scripted presses instead of reaching the simulator, and the
// button input byte (read with `1e 83`) shows each press held for its
// scripted duration (recorded traces contain no button traffic). Testing
// only.
type ButtonInjector struct {
	mu        sync.Mutex
	queue     []scriptedPress
	held      byte // input byte while the current press is held
	releaseAt time.Time
}

type scriptedPress struct {
	button v370.Button
	hold   time.Duration
}

// ParseButtons parses "pdf,email,pdf:2s,printer:1.5s,power" into an injector: a
// button name, optionally with how long it is held (default: a short
// press of v370.PressDebounce).
func ParseButtons(list string) (*ButtonInjector, error) {
	inj := &ButtonInjector{}
	names := map[string]v370.Button{"pdf": v370.ButtonPDF, "email": v370.ButtonEmail, "printer": v370.ButtonPrinter, "power": v370.ButtonPower}
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		name, dur, timed := strings.Cut(n, ":")
		b, ok := names[name]
		if !ok {
			return nil, fmt.Errorf("unknown button %q", n)
		}
		p := scriptedPress{button: b, hold: v370.PressDebounce}
		if timed {
			d, err := time.ParseDuration(dur)
			if err != nil || d < v370.PressDebounce || d > time.Minute {
				return nil, fmt.Errorf("button %q: bad hold time", n)
			}
			p.hold = d
		}
		inj.queue = append(inj.queue, p)
	}
	return inj, nil
}

// Wrap returns an opener whose transports answer 1e 85 from the queue.
func (inj *ButtonInjector) Wrap(open Opener) Opener {
	return func() (usb.Transport, string, error) {
		tp, desc, err := open()
		if err != nil {
			return nil, "", err
		}
		return &injectingTransport{Transport: tp, inj: inj}, desc + " + injected buttons", nil
	}
}

// next reports the next press and starts holding it.
func (inj *ButtonInjector) next() byte {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if len(inj.queue) == 0 {
		return 0
	}
	p := inj.queue[0]
	inj.queue = inj.queue[1:]
	inj.held = 0xF0 &^ (0x08 << byte(p.button))
	inj.releaseAt = time.Now().Add(p.hold - v370.PressDebounce)
	return byte(p.button)
}

// input is the button input byte now (0xF0 = nothing held).
func (inj *ButtonInjector) input() byte {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if inj.held == 0 || !time.Now().Before(inj.releaseAt) {
		return 0xF0
	}
	return inj.held
}

type injectingTransport struct {
	usb.Transport
	inj      *ButtonInjector
	pending  []byte
	memParam bool // 1e 83 acknowledged, parameter expected
}

var (
	cmdButton = []byte{0x1e, 0x85}
	cmdMemory = []byte{0x1e, 0x83}
)

const ramButtonInput = 0x452

func (t *injectingTransport) BulkOut(ctx context.Context, ep uint8, data []byte) (int, error) {
	switch {
	case bytes.Equal(data, cmdButton):
		t.pending = []byte{t.inj.next()}
		return len(data), nil
	case bytes.Equal(data, cmdMemory):
		t.pending, t.memParam = []byte{0x06}, true
		return len(data), nil
	case t.memParam:
		t.memParam = false
		if len(data) != 8 || data[0] != 0x08 {
			return 0, fmt.Errorf("injected buttons: 1e 83 parameter %x", data)
		}
		addr := binary.LittleEndian.Uint32(data[1:])
		n := int(data[5]) | int(data[6])<<8 | int(data[7])<<16
		out := make([]byte, n)
		for i := range out {
			if addr+uint32(i) == ramButtonInput {
				out[i] = t.inj.input()
			}
		}
		t.pending = out
		return len(data), nil
	}
	return t.Transport.BulkOut(ctx, ep, data)
}

func (t *injectingTransport) BulkIn(ctx context.Context, ep uint8, max int) ([]byte, error) {
	if t.pending != nil {
		b := t.pending
		t.pending = nil
		return b, nil
	}
	return t.Transport.BulkIn(ctx, ep, max)
}
