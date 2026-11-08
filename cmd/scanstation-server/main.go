// Command scanstation-server is the scanSTATION server: a REST API for
// scanning and, by default, scan-on-button-press. Both use the
// postprocessing and button settings of ~/.config/scanstation/config.json
// (default: crop to the requested size).
//
// Usage:
//
//	scanstation-server [flags]
//	scanstation-server --addr :8080            # listen on port 8080
//	scanstation-server -o ~/scans              # output directory (API scans)
//	scanstation-server --driver epsonscan2     # always use the vendor app (no buttons)
//	scanstation-server --buttons=false         # do not watch the buttons
//
// Drivers are compiled in with build tags (internal/drivers); --driver auto
// (default) uses every compiled-in driver in priority order.
//
// API endpoints:
//
//	GET  /api/health
//	GET  /api/devices
//	GET  /api/status
//	POST /api/scan                       — single-page scan
//	POST /api/scan/multi                 — start multi-page session
//	POST /api/scan/multi/{id}/next       — scan next page
//	POST /api/scan/multi/{id}/finish     — combine pages into one PDF
//	GET  /api/scan/multi/{id}            — session status
//	DELETE /api/scan/multi/{id}          — cancel session
//	GET  /api/files/{filename}           — download a scanned file
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/api"
	"github.com/scanSTATION/scanSTATION-server/internal/buttons"
	"github.com/scanSTATION/scanSTATION-server/internal/config"
	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	_ "github.com/scanSTATION/scanSTATION-server/internal/drivers" // compiled-in drivers (build tags)
)

func main() {
	addr := flag.String("addr", ":8080", "listen address (host:port)")
	outputDir := flag.String("o", "", "output directory for API scans (default: ~/Desktop)")
	watch := flag.Bool("buttons", true, "watch the scanner buttons and run the configured actions")
	cfgPath := flag.String("config", config.DefaultPath(), "configuration file (created with defaults if missing)")
	interval := flag.Duration("button-interval", 100*time.Millisecond, "button polling interval")
	var bf driver.Flags
	bf.Register(flag.CommandLine)
	flag.Parse()

	dir := *outputDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Error getting home dir: %v", err)
		}
		dir = filepath.Join(home, "Desktop")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("Error creating output dir %s: %v", dir, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	b, err := bf.Build(driver.Env{WorkDir: driver.DefaultWorkDir(), Log: logger})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	if c, ok := b.(io.Closer); ok {
		defer c.Close()
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	post, err := cfg.Pipeline()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	srv := api.NewServer(b, dir)
	srv.SetPostprocessing(post)
	if post.Empty() {
		log.Printf("Postprocessing: none (scans are stored as scanned)")
	} else {
		log.Printf("Postprocessing: %s (config: %s)", strings.Join(post.Names(), ", "), *cfgPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if bw, ok := b.(driver.ButtonWatcher); ok && *watch {
		log.Printf("Watching scanner buttons (config: %s)", *cfgPath)
		ctl := buttons.New(cfg, srv.Scan, logger, nil)
		srv.SetButtonDocuments(ctl.Documents)
		defer ctl.Close() // finish open multi-page documents
		go func() {
			if err := bw.WatchButtons(ctx, *interval, ctl.OnPress); err != nil {
				logger.Error("button watcher stopped", "err", err)
			}
		}()
	}

	log.Printf("scanSTATION server listening on %s (driver: %s)", *addr, b.Name())
	log.Printf("Output directory: %s", dir)
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		log.Printf("Shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}
}
