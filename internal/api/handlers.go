package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/scanSTATION/scanSTATION-server/internal/driver"
	"github.com/scanSTATION/scanSTATION-server/internal/files"
	"github.com/scanSTATION/scanSTATION-server/internal/pdfutil"
	"github.com/scanSTATION/scanSTATION-server/internal/scan"
)

// healthCheck responds to GET /api/health.
func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// scanTimeout bounds one scan. Scans deliberately do not follow the request
// context: aborting a direct USB session halfway can hang the scanner
// (docs/protocol.md §2), so a disconnecting client does not cancel it.
const scanTimeout = 10 * time.Minute

func scanContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), scanTimeout)
}

// moveResult moves the image and its companion files into the output
// directory and returns the image's final path.
func (s *Server) moveResult(res driver.Result) (string, error) {
	var final string
	for i, f := range res.Files() {
		p, err := files.MoveScannedFile(f, s.outputDir, "")
		if err != nil {
			return "", err
		}
		if i == 0 {
			final = p
		}
	}
	return final, nil
}

// removeResult deletes the image and its companion files.
func removeResult(res driver.Result) {
	for _, f := range res.Files() {
		os.Remove(f)
	}
}

// listDevices responds to GET /api/devices.
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	s.scanMu.Lock()
	devices, err := s.backend.ListDevices(r.Context())
	s.scanMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing devices: %v", err))
		return
	}

	resp := DeviceListResponse{Devices: make([]DeviceInfo, len(devices))}
	for i, d := range devices {
		resp.Devices[i] = DeviceInfo{Name: d.Name, ModelID: d.ModelID}
	}
	writeJSON(w, http.StatusOK, resp)
}

// getStatus responds to GET /api/status.
// 200 {"ready":true,"status":"ready"} when the scanner is fine,
// 200 {"ready":false,"status":"<caution>"} when it reports a caution,
// 503 when the vendor app reports an error (e.g. scanner not reachable).
func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	s.scanMu.Lock() // the status query opens the device, like a scan
	st, err := s.backend.Status(r.Context())
	s.scanMu.Unlock()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("getting status: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, StatusResponse{Ready: st.Ready, Status: st.Message})
}

// singleScan responds to POST /api/scan.
// It performs one scan and moves the output file to the output directory.
func (s *Server) singleScan(w http.ResponseWriter, r *http.Request) {
	params, err := decodeJSON[ScanParams](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}
	params.ApplyDefaults()

	req, err := parseScanParams(params)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := scanContext(r)
	defer cancel()
	res, err := s.doScan(ctx, req)
	if err != nil {
		writeError(w, scanErrorStatus(err), err.Error())
		return
	}

	// Move the file (and its metadata) to the output directory.
	finalPath, err := s.moveResult(res)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("moving scanned file: %v", err))
		return
	}

	filename := filepath.Base(finalPath)
	writeJSON(w, http.StatusOK, ScanResponse{
		File:     finalPath,
		Filename: filename,
		Download: downloadURL(filename),
		Message:  fmt.Sprintf("Scanned at %d DPI, %s, %s, quality %d", params.DPI, params.Size, params.Color, params.Quality),
		Backend:  res.Backend,
	})
}

// multiStart responds to POST /api/scan/multi.
// It creates a new session and scans the first page.
func (s *Server) multiStart(w http.ResponseWriter, r *http.Request) {
	params, err := decodeJSON[ScanParams](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}
	params.ApplyDefaults()

	var warnings []string
	if params.Format != "pdf" && params.Format != "" {
		warnings = append(warnings, fmt.Sprintf("multi-page mode always outputs PDF; format %q is ignored", params.Format))
	}

	// Verify pdfunite is available before starting.
	if err := pdfutil.CheckAvailable(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	req, err := parseScanParams(params)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Multi-page always uses PDF.
	req.Format = scan.FormatPDF

	sess := s.sessions.Create(params)
	sess.Lock()
	defer sess.Unlock()

	// Scan page 1.
	pagePrefix := fmt.Sprintf("%s_%03d", params.Prefix, 1)
	req.FileNamePrefix = pagePrefix

	ctx, cancel := scanContext(r)
	defer cancel()
	res, err := s.doScan(ctx, req)
	if err != nil {
		sess.Status = StatusError
		sess.Error = err.Error()
		writeError(w, scanErrorStatus(err), fmt.Sprintf("scan failed for page 1: %v", err))
		return
	}

	sess.PagePDFs = append(sess.PagePDFs, res.Path)
	sess.PageExtras = append(sess.PageExtras, res.Extra...)
	sess.Backend = res.Backend
	sess.Status = StatusWaiting

	writeJSON(w, http.StatusCreated, MultiStartResponse{
		SessionID: sess.ID,
		Page:      1,
		Status:    string(StatusWaiting),
		Warnings:  warnings,
		Backend:   res.Backend,
	})
}

// multiNext responds to POST /api/scan/multi/{id}/next.
// It scans the next page in an existing session.
func (s *Server) multiNext(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.sessions.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	sess.Lock()
	defer sess.Unlock()

	if sess.Status != StatusWaiting {
		writeError(w, http.StatusConflict, fmt.Sprintf("session is %s, cannot scan next page", sess.Status))
		return
	}

	pageNum := len(sess.PagePDFs) + 1
	sess.Status = StatusScanning

	req, err := parseScanParams(sess.Params)
	if err != nil {
		sess.Status = StatusError
		sess.Error = err.Error()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Format = scan.FormatPDF
	req.FileNamePrefix = fmt.Sprintf("%s_%03d", sess.Params.Prefix, pageNum)

	ctx, cancel := scanContext(r)
	defer cancel()
	res, err := s.doScan(ctx, req)
	if err != nil {
		sess.Status = StatusError
		sess.Error = err.Error()
		writeError(w, scanErrorStatus(err), fmt.Sprintf("scan failed for page %d: %v", pageNum, err))
		return
	}

	sess.PagePDFs = append(sess.PagePDFs, res.Path)
	sess.PageExtras = append(sess.PageExtras, res.Extra...)
	sess.Status = StatusWaiting

	writeJSON(w, http.StatusOK, MultiNextResponse{
		SessionID: sess.ID,
		Page:      pageNum,
		Status:    string(StatusWaiting),
		Backend:   res.Backend,
	})
}

// multiFinish responds to POST /api/scan/multi/{id}/finish.
// It combines all scanned page PDFs into a single multi-page PDF.
func (s *Server) multiFinish(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.sessions.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	sess.Lock()
	defer sess.Unlock()

	if sess.Status != StatusWaiting {
		writeError(w, http.StatusConflict, fmt.Sprintf("session is %s, cannot finish", sess.Status))
		return
	}

	if len(sess.PagePDFs) == 0 {
		writeError(w, http.StatusBadRequest, "no pages were scanned")
		return
	}

	// Combine all page PDFs into one.
	finalName := sess.Params.Prefix + ".pdf"
	finalPath := filepath.Join(s.outputDir, finalName)

	if err := pdfutil.CombinePDFs(sess.PagePDFs, finalPath); err != nil {
		sess.Status = StatusError
		sess.Error = err.Error()
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("combining PDFs: %v", err))
		return
	}

	// Clean up individual page PDFs and their metadata.
	for _, f := range append(sess.PagePDFs, sess.PageExtras...) {
		os.Remove(f)
	}

	sess.Status = StatusFinished
	sess.FinalFile = finalPath

	filename := filepath.Base(finalPath)
	writeJSON(w, http.StatusOK, MultiFinishResponse{
		SessionID: sess.ID,
		Pages:     len(sess.PagePDFs),
		File:      finalPath,
		Filename:  filename,
		Download:  downloadURL(filename),
	})
}

// multiStatus responds to GET /api/scan/multi/{id}.
func (s *Server) multiStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.sessions.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	sess.Lock()
	defer sess.Unlock()

	resp := SessionStatusResponse{
		SessionID: sess.ID,
		Status:    string(sess.Status),
		Pages:     len(sess.PagePDFs),
		Error:     sess.Error,
		CreatedAt: sess.CreatedAt.Format(time.RFC3339),
	}
	if sess.FinalFile != "" {
		resp.File = sess.FinalFile
		resp.Filename = filepath.Base(sess.FinalFile)
	}
	writeJSON(w, http.StatusOK, resp)
}

// multiCancel responds to DELETE /api/scan/multi/{id}.
// It cleans up any individual page PDFs and removes the session.
func (s *Server) multiCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.sessions.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	sess.Lock()
	// Clean up individual page PDFs and their metadata.
	for _, f := range append(sess.PagePDFs, sess.PageExtras...) {
		os.Remove(f)
	}
	sess.Status = StatusCancelled
	sess.Unlock()

	s.sessions.Delete(id)

	writeJSON(w, http.StatusOK, map[string]string{
		"session_id": id,
		"status":     "cancelled",
	})
}

// downloadFile responds to GET /api/files/{filename}.
// It serves a file from the output directory, preventing path traversal.
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	path, err := safeJoin(s.outputDir, filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	if _, err := os.Stat(path); err != nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	http.ServeFile(w, r, path)
}
