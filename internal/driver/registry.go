package driver

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Env is what a driver gets when it is built.
type Env struct {
	// WorkDir holds results before the caller moves them (private, 0700).
	WorkDir string
	Log     *slog.Logger
}

// Registration describes a compiled-in driver.
type Registration struct {
	Name        string
	Description string
	// Priority orders the auto chain (lower first).
	Priority int
	// Flags registers the driver's own command-line flags (prefixed with
	// the driver name) and returns the builder that uses their values.
	Flags func(fs *flag.FlagSet) func(Env) (Backend, error)
}

var (
	regMu sync.Mutex
	regs  = map[string]Registration{}
)

// Register adds a driver; called from the driver package's init().
func Register(r Registration) {
	regMu.Lock()
	defer regMu.Unlock()
	if r.Name == "" || r.Name == Auto || r.Flags == nil {
		panic("driver: invalid registration " + r.Name)
	}
	if _, dup := regs[r.Name]; dup {
		panic("driver: duplicate registration " + r.Name)
	}
	regs[r.Name] = r
}

// Registered returns the compiled-in drivers in priority order.
func Registered() []Registration {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]Registration, 0, len(regs))
	for _, r := range regs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Flags are the driver options shared by the CLI and the server.
type Flags struct {
	Driver   string
	builders map[string]func(Env) (Backend, error)
}

// Register adds --driver and every compiled-in driver's flags to fs.
func (f *Flags) Register(fs *flag.FlagSet) {
	var names []string
	f.builders = map[string]func(Env) (Backend, error){}
	for _, r := range Registered() {
		names = append(names, r.Name+" ("+r.Description+")")
		f.builders[r.Name] = r.Flags(fs)
	}
	help := "scanner driver: auto (all compiled-in drivers, first that supports the request)"
	if len(names) > 0 {
		help += " or one of: " + strings.Join(names, ", ")
	} else {
		help += " — this binary contains NO drivers (build with -tags, e.g. v370)"
	}
	fs.StringVar(&f.Driver, "driver", Auto, help)
}

// Build creates the selected backend.
func (f *Flags) Build(env Env) (Backend, error) {
	regs := Registered()
	if f.builders == nil {
		return nil, fmt.Errorf("driver flags not registered")
	}
	if len(regs) == 0 {
		return nil, fmt.Errorf("this binary contains no scanner drivers (build with -tags, e.g. -tags v370)")
	}
	if f.Driver != Auto && f.Driver != "" {
		b, ok := f.builders[f.Driver]
		if !ok {
			return nil, fmt.Errorf("unknown driver %q (compiled in: %s)", f.Driver, names(regs))
		}
		return b(env)
	}
	var chain []Backend
	for _, r := range regs {
		b, err := f.builders[r.Name](env)
		if err != nil {
			closeAll(chain)
			return nil, fmt.Errorf("driver %s: %w", r.Name, err)
		}
		chain = append(chain, b)
	}
	if len(chain) == 1 {
		return chain[0], nil
	}
	return &Chain{Backends: chain, Log: env.Log}, nil
}

func names(regs []Registration) string {
	var s []string
	for _, r := range regs {
		s = append(s, r.Name)
	}
	return strings.Join(s, ", ")
}

func closeAll(bs []Backend) {
	for _, b := range bs {
		if c, ok := b.(io.Closer); ok {
			c.Close()
		}
	}
}

// DefaultWorkDir is a private per-user directory for intermediate results.
func DefaultWorkDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "scanstation", "work")
	}
	return filepath.Join(os.TempDir(), "scanstation-work")
}
