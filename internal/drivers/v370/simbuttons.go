package v370drv

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/v370"
)

// ButtonInjector wraps simulated scanners so that `1e 85` (button query)
// answers with scripted presses instead of reaching the replay (recorded
// traces contain no button traffic). Testing only.
type ButtonInjector struct {
	mu    sync.Mutex
	queue []byte
}

// ParseButtons parses "pdf,email,printer" into an injector.
func ParseButtons(list string) (*ButtonInjector, error) {
	inj := &ButtonInjector{}
	for _, n := range strings.Split(list, ",") {
		switch strings.TrimSpace(n) {
		case "pdf":
			inj.queue = append(inj.queue, byte(v370.ButtonPDF))
		case "email":
			inj.queue = append(inj.queue, byte(v370.ButtonEmail))
		case "printer":
			inj.queue = append(inj.queue, byte(v370.ButtonPrinter))
		case "":
		default:
			return nil, fmt.Errorf("unknown button %q", n)
		}
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

func (inj *ButtonInjector) next() byte {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if len(inj.queue) == 0 {
		return 0
	}
	b := inj.queue[0]
	inj.queue = inj.queue[1:]
	return b
}

type injectingTransport struct {
	usb.Transport
	inj     *ButtonInjector
	pending []byte
}

var cmdButton = []byte{0x1e, 0x85}

func (t *injectingTransport) BulkOut(ctx context.Context, ep uint8, data []byte) (int, error) {
	if bytes.Equal(data, cmdButton) {
		t.pending = []byte{t.inj.next()}
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
