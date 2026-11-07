// Package pdfutil merges individual PDF files into a single multi-page PDF
// using the poppler-utils command-line tool `pdfunite`.
//
// This is a thin wrapper around an external system command — no Go PDF
// library is needed. Install poppler-utils on the host:
//
//	Debian/Ubuntu: apt-get install poppler-utils
//	Arch Linux:    pacman -S poppler
//	Fedora:        dnf install poppler-utils
package pdfutil

import (
	"bytes"
	"fmt"
	"os/exec"
)

// CheckAvailable verifies that pdfunite (poppler-utils) is installed and
// reachable via PATH. Returns an error with install instructions if not.
func CheckAvailable() error {
	path, err := exec.LookPath("pdfunite")
	if err != nil {
		return fmt.Errorf("pdfunite not found in PATH — install poppler-utils")
	}
	_ = path
	return nil
}

// BuildArgs constructs the argument vector for pdfunite without executing it.
// This is separated from CombinePDFs so it can be unit-tested without the
// binary being installed.
func BuildArgs(inputPDFs []string, outputPath string) []string {
	args := make([]string, 0, len(inputPDFs)+1)
	args = append(args, inputPDFs...)
	args = append(args, outputPath)
	return args
}

// CombinePDFs merges multiple individual PDF files into a single multi-page
// PDF using pdfunite. The input files are not modified or removed — the caller
// is responsible for cleanup.
//
// pdfunite usage: pdfunite input1.pdf input2.pdf ... output.pdf
func CombinePDFs(inputPDFs []string, outputPath string) error {
	if len(inputPDFs) == 0 {
		return fmt.Errorf("no input PDFs to combine")
	}
	if outputPath == "" {
		return fmt.Errorf("output path is empty")
	}
	if err := CheckAvailable(); err != nil {
		return err
	}

	cmd := exec.Command("pdfunite", BuildArgs(inputPDFs, outputPath)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pdfunite failed: %w\nstderr: %s", err, stderr.String())
	}
	return nil
}
