// Command scanstation is the scanSTATION command-line tool: scan, list
// scanners, query status, watch the panel buttons.
//
// The scanner drivers it contains are chosen at build time (build tags, see
// internal/drivers). By default every compiled-in driver is tried in
// priority order (--driver auto); --driver NAME forces one.
//
// Usage:
//
//	scanstation [flags]
//	scanstation --list                         # list available scanners
//	scanstation --status                       # get scanner status
//	scanstation --dpi 300 --size a4            # scan at 300 DPI, A4 (default)
//	scanstation --format png                   # lossless
//	scanstation --driver epsonscan2 --dpi 75   # The vendor app
//	scanstation --multi --quality 100          # multi-page PDF (interactive)
//	scanstation --watch                        # scan on button press
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/buttons"
	"github.com/scanSTATION/scanSTATION-server/internal/config"
	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	_ "github.com/scanSTATION/scanSTATION-server/internal/drivers" // compiled-in drivers (build tags)
	"github.com/scanSTATION/scanSTATION-server/internal/files"
	"github.com/scanSTATION/scanSTATION-server/internal/pdfutil"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

func main() {
	// Flags
	list := flag.Bool("list", false, "list available scanner devices")
	status := flag.Bool("status", false, "get scanner device status")
	dpi := flag.Int("dpi", 300, "scan resolution in DPI (driver-dependent; v370: A4 100, 200, 300, 600; A5 1200 via -size user)")
	format := flag.String("format", "jpeg", "output format: jpeg, png, pdf, raw")
	color := flag.String("color", "rgb", "color mode: rgb, gray, lineart (gray/lineart: epsonscan2 driver)")
	size := flag.String("size", "a4", "paper size: a4, letter, max, user")
	widthMM := flag.Float64("width-mm", 210, "custom scan width in mm (used with -size user)")
	heightMM := flag.Float64("height-mm", 297, "custom scan height in mm (used with -size user)")
	quality := flag.Int("quality", 85, "JPEG quality (1–100; use 100 for maximum quality)")
	outputDir := flag.String("o", "", "output directory (default: ~/Desktop)")
	prefix := flag.String("prefix", "scan", "filename prefix for the scan output")
	multi := flag.Bool("multi", false, "multi-page mode: scan repeatedly, combine into a single PDF")
	timestamp := flag.Bool("timestamp", false, "append Unix timestamp suffix to the output filename")
	keepPages := flag.Bool("keep-pages", false, "in --multi mode, keep individual page PDFs (otherwise cleaned up)")
	watch := flag.Bool("watch", false, "watch the scanner buttons and run the configured actions until Ctrl-C")
	cfgPath := flag.String("config", config.DefaultPath(), "configuration file for --watch (created with defaults if missing)")
	interval := flag.Duration("button-interval", 100*time.Millisecond, "button polling interval for --watch")
	verbose := flag.Bool("verbose", false, "detailed logging (scanner protocol steps)")
	var bf driver.Flags
	bf.Register(flag.CommandLine)
	flag.Parse()

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	b, err := bf.Build(driver.Env{WorkDir: driver.DefaultWorkDir(), Log: logger})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if c, ok := b.(io.Closer); ok {
		defer c.Close()
	}

	// Handle --watch
	if *watch {
		bw, ok := b.(driver.ButtonWatcher)
		if !ok {
			log.Fatalf("Error: driver %s cannot watch buttons", b.Name())
		}
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			log.Fatalf("Error: %v", err)
		}
		info := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		fmt.Printf("Watching scanner buttons (config: %s) — Ctrl-C to stop\n", *cfgPath)
		if err := bw.WatchButtons(ctx, *interval, buttons.Handler(cfg, b.Scan, info, nil)); err != nil {
			log.Fatalf("Error: %v", err)
		}
		fmt.Println("Stopped.")
		return
	}

	// Handle --list
	if *list {
		devices, err := b.ListDevices(ctx)
		if err != nil {
			log.Fatalf("Error listing devices: %v", err)
		}
		if len(devices) == 0 {
			fmt.Println("No scanner devices found.")
			os.Exit(1)
		}
		fmt.Println("Available scanner devices:")
		for i, d := range devices {
			fmt.Printf("  [%d] %s (Model: %s)\n", i, d.Name, d.ModelID)
		}
		return
	}

	// Handle --status
	if *status {
		st, err := b.Status(ctx)
		if err != nil {
			log.Fatalf("Error getting status: %v", err)
		}
		fmt.Printf("Scanner status: %s\n", st.Message)
		if !st.Ready {
			os.Exit(1)
		}
		return
	}

	fmtMap := map[string]scan.Format{
		"jpeg": scan.FormatJPEG,
		"jpg":  scan.FormatJPEG,
		"png":  scan.FormatPNG,
		"pdf":  scan.FormatPDF,
		"raw":  scan.FormatRaw,
	}
	imgFormat, ok := fmtMap[*format]
	if !ok {
		log.Fatalf("Unknown format %q (use: jpeg, png, pdf, raw)", *format)
	}
	colorMap := map[string]scan.ColorMode{
		"rgb":     scan.ColorRGB,
		"color":   scan.ColorRGB,
		"gray":    scan.ColorGrayscale,
		"grey":    scan.ColorGrayscale,
		"lineart": scan.ColorLineArt,
		"bw":      scan.ColorLineArt,
	}
	colorMode, ok := colorMap[*color]
	if !ok {
		log.Fatalf("Unknown color mode %q (use: rgb, gray, lineart)", *color)
	}
	sizeMap := map[string]scan.DocumentSize{
		"a4":     scan.SizeA4,
		"letter": scan.SizeLetter,
		"legal":  scan.SizeLegal,
		"max":    scan.SizeMaxArea,
		"user":   scan.SizeUserDef,
	}
	docSize, ok := sizeMap[*size]
	if !ok {
		log.Fatalf("Unknown size %q (use: a4, letter, max, user)", *size)
	}

	finalDir := *outputDir
	if finalDir == "" {
		home, _ := os.UserHomeDir()
		finalDir = filepath.Join(home, "Desktop")
	}
	effectivePrefix := *prefix
	if *timestamp {
		effectivePrefix = fmt.Sprintf("%s_%d", *prefix, time.Now().Unix())
	}
	req := scan.ScanRequest{
		Resolution:     *dpi,
		Format:         imgFormat,
		ColorMode:      colorMode,
		Size:           docSize,
		WidthMM:        *widthMM,
		HeightMM:       *heightMM,
		FileNamePrefix: effectivePrefix,
		JpegQuality:    *quality,
	}
	if err := scan.Validate(req); err != nil {
		log.Fatalf("Error: %v", err)
	}

	if *multi {
		if imgFormat != scan.FormatPDF {
			fmt.Fprintf(os.Stderr, "Warning: --multi always outputs PDF; --format %q is ignored.\n\n", *format)
		}
		req.Format = scan.FormatPDF
		if err := runMultiPageScan(ctx, b, req, finalDir, *keepPages); err != nil {
			log.Fatalf("Multi-page scan failed: %v", err)
		}
		return
	}

	fmt.Printf("Scanning at %d DPI, %s, %s, quality %d...\n", *dpi, *size, *color, *quality)
	res, err := b.Scan(ctx, req)
	if err != nil {
		log.Fatalf("Scan failed: %v", err)
	}
	finalPath, err := moveResult(res, finalDir)
	if err != nil {
		log.Fatalf("Error moving scanned file: %v", err)
	}
	fmt.Printf("Scanned file: %s (driver: %s)\n", finalPath, res.Backend)
}

// moveResult moves the image and its companion files into dir.
func moveResult(res driver.Result, dir string) (string, error) {
	var final string
	for i, f := range res.Files() {
		p, err := files.MoveScannedFile(f, dir, "")
		if err != nil {
			return "", err
		}
		if i == 0 {
			final = p
		}
	}
	return final, nil
}

// runMultiPageScan scans pages until the user presses 'e' and merges them
// into one PDF with pdfunite (poppler-utils).
func runMultiPageScan(ctx context.Context, b driver.Backend, req scan.ScanRequest, outputDir string, keepPages bool) error {
	if err := pdfutil.CheckAvailable(); err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}
	prefix := req.FileNamePrefix
	var pages []driver.Result
	cleanup := func() {
		for _, p := range pages {
			for _, f := range p.Files() {
				os.Remove(f)
			}
		}
	}

	for pageNum := 1; ; pageNum++ {
		r := req
		r.FileNamePrefix = fmt.Sprintf("%s_%03d", prefix, pageNum)
		fmt.Printf("Scanning page %d (DPI %d, quality %d)...\n", pageNum, r.Resolution, r.JpegQuality)
		res, err := b.Scan(ctx, r)
		if err != nil {
			cleanup()
			return fmt.Errorf("scan failed for page %d: %w", pageNum, err)
		}
		pages = append(pages, res)
		fmt.Printf("Page %d scanned: %s (driver: %s)\n\n", pageNum, filepath.Base(res.Path), res.Backend)

		next, err := promptNextOrEnd()
		if err != nil {
			cleanup()
			return fmt.Errorf("reading input: %w", err)
		}
		if !next {
			break
		}
		fmt.Println()
	}

	finalPDF := filepath.Join(outputDir, prefix+".pdf")
	fmt.Printf("\nCombining %d page(s) into %s...\n", len(pages), filepath.Base(finalPDF))
	var pdfs []string
	for _, p := range pages {
		pdfs = append(pdfs, p.Path)
	}
	if err := pdfutil.CombinePDFs(pdfs, finalPDF); err != nil {
		cleanup()
		return fmt.Errorf("combining PDFs: %w", err)
	}
	if keepPages {
		for _, p := range pages {
			if _, err := moveResult(p, outputDir); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not move page %s: %v\n", p.Path, err)
			}
		}
		fmt.Printf("Individual page PDFs kept in %s\n", outputDir)
	} else {
		cleanup()
	}
	fmt.Printf("Multi-page PDF: %s\n", finalPDF)
	return nil
}

func promptNextOrEnd() (bool, error) {
	for {
		fmt.Print("Press 'n' for next page, 'e' to end: ")
		key, err := readSingleKey()
		if err != nil {
			return false, err
		}
		fmt.Println()
		switch key {
		case 'n', 'N':
			return true, nil
		case 'e', 'E':
			return false, nil
		default:
			fmt.Println("Invalid key. Please press 'n' or 'e'.")
		}
	}
}

// readSingleKey reads a single byte from stdin without requiring Enter.
// It sets the terminal to raw mode via stty, then restores it.
// If raw mode is unavailable (e.g. non-interactive stdin), it falls back
// to line-based input.
func readSingleKey() (byte, error) {
	// Try raw mode via stty for single-keypress input.
	rawCmd := exec.Command("stty", "raw", "-echo")
	rawCmd.Stdin = os.Stdin
	rawMode := rawCmd.Run() == nil

	if rawMode {
		defer func() {
			restoreCmd := exec.Command("stty", "sane")
			restoreCmd.Stdin = os.Stdin
			restoreCmd.Run()
		}()
	}

	var b [1]byte
	n, err := os.Stdin.Read(b[:])
	if err != nil {
		return 0, fmt.Errorf("reading key: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("no input")
	}
	return b[0], nil
}
