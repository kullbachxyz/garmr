package web

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"garmr/internal/fitx"
	"garmr/internal/importlog"
	"garmr/internal/store"
)

// simple lock so only one manual import runs at a time
var importBusy = make(chan struct{}, 1)

type importResp struct {
	FoundFiles int      `json:"found_files"`
	Imported   int      `json:"imported"`
	Duplicates int      `json:"duplicates"`
	Errors     []string `json:"errors,omitempty"`
	Message    string   `json:"message"`
}

type importPageVM struct {
	CurrentUser *userView
}

type uploadResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
	Error      string `json:"error,omitempty"`
	Imported   int    `json:"imported"`
	Duplicates int    `json:"duplicates"`
	Failed     int    `json:"failed"`
}

// POST /api/import  -> run a single scan now
func (s *Server) handleImportNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	select {
	case importBusy <- struct{}{}:
		defer func() { <-importBusy }()
	default:
		http.Error(w, "import already running", http.StatusConflict)
		return
	}

	if s.im == nil {
		http.Error(w, "importer not available", http.StatusServiceUnavailable)
		return
	}

	importlog.Printf("import: triggered via web")
	sum, _ := s.im.ScanOnce()

	// Create meaningful message based on results
	var message string
	if sum.FoundFiles == 0 {
		message = "No devices or activity files found. Check if Garmin device is connected."
	} else if sum.Imported == 0 && sum.Duplicates > 0 {
		message = fmt.Sprintf("Found %d files, but all were duplicates (already imported).", sum.FoundFiles)
	} else if sum.Imported == 0 && len(sum.Errors) > 0 {
		message = fmt.Sprintf("Found %d files, but failed to import any. Check logs for details.", sum.FoundFiles)
	} else if sum.Imported > 0 {
		if sum.Duplicates > 0 {
			message = fmt.Sprintf("Import completed: %d new activities imported, %d duplicates skipped from %d files.", sum.Imported, sum.Duplicates, sum.FoundFiles)
		} else {
			message = fmt.Sprintf("Import completed: %d new activities imported from %d files.", sum.Imported, sum.FoundFiles)
		}
	} else {
		message = fmt.Sprintf("Scan completed: found %d files, but no new activities to import.", sum.FoundFiles)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(importResp{
		FoundFiles: sum.FoundFiles,
		Imported:   sum.Imported,
		Duplicates: sum.Duplicates,
		Errors:     sum.Errors,
		Message:    message,
	})
}

// GET /api/logs  -> Server-Sent Events (live import logs)
func (s *Server) handleLogsSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// send a small snapshot first so the pane isn't empty
	for _, line := range importlog.Snapshot(50) {
		_, _ = w.Write([]byte("data: " + line + "\n\n"))
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	ch := importlog.Subscribe()
	defer importlog.Unsubscribe(ch)

	notify := r.Context().Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-notify:
			return
		case line := <-ch:
			_, _ = w.Write([]byte("data: " + line + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		case <-ticker.C:
			// keep-alive comment (helps proxies/browsers keep the connection)
			_, _ = w.Write([]byte(": ping\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

func (s *Server) handleImportPage(w http.ResponseWriter, r *http.Request) {
	vm := importPageVM{
		CurrentUser: s.currentUser(r),
	}
	if err := s.tplImport.ExecuteTemplate(w, "layout", vm); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (32MB max)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		log.Printf("upload: parse form error: %v", err)
		writeUploadError(w, "Failed to parse upload form")
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeUploadError(w, "No files uploaded")
		return
	}

	importlog.Printf("upload: processing %d files", len(files))

	var imported, duplicates, failed int

	for _, fileHeader := range files {
		if !strings.HasSuffix(strings.ToLower(fileHeader.Filename), ".fit") {
			importlog.Printf("upload: skipping non-FIT file: %s", fileHeader.Filename)
			failed++
			continue
		}

		file, err := fileHeader.Open()
		if err != nil {
			importlog.Printf("upload: failed to open file %s: %v", fileHeader.Filename, err)
			failed++
			continue
		}

		// Read file data
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			importlog.Printf("upload: failed to read file %s: %v", fileHeader.Filename, err)
			failed++
			continue
		}

		// Calculate file hash for duplicate detection
		hash := fmt.Sprintf("%x", sha256.Sum256(data))

		// Check for duplicate by hash
		isDuplicate, err := s.isFileHashDuplicate(hash)
		if err != nil {
			importlog.Printf("upload: failed to check duplicate for %s: %v", fileHeader.Filename, err)
			failed++
			continue
		}

		if isDuplicate {
			importlog.Printf("upload: duplicate file detected: %s (hash: %s)", fileHeader.Filename, hash[:12])
			duplicates++
			continue
		}

		// Process FIT file
		if err := s.processFITFile(data, fileHeader.Filename, hash); err != nil {
			// Check if it's a duplicate detected during processing
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") ||
				strings.Contains(strings.ToLower(err.Error()), "already exists") {
				importlog.Printf("upload: duplicate detected during processing: %s", fileHeader.Filename)
				duplicates++
				continue
			}
			importlog.Printf("upload: failed to process FIT file %s: %v", fileHeader.Filename, err)
			failed++
			continue
		}

		importlog.Printf("upload: successfully imported: %s", fileHeader.Filename)
		imported++
	}

	response := uploadResponse{
		Success:    true,
		Imported:   imported,
		Duplicates: duplicates,
		Failed:     failed,
		Message:    fmt.Sprintf("Processed %d files: %d imported, %d duplicates, %d failed", len(files), imported, duplicates, failed),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) isFileHashDuplicate(hash string) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM activities WHERE file_hash = ?", hash).Scan(&count)
	return count > 0, err
}

func (s *Server) processFITFile(data []byte, filename, hash string) error {
	// Save raw file to storage first
	rawPath := filepath.Join(s.cfg.RawStore, fmt.Sprintf("upload_%s_%s.fit",
		time.Now().Format("20060102_150405"), filename))

	if err := os.MkdirAll(filepath.Dir(rawPath), 0755); err != nil {
		return fmt.Errorf("create raw store dir: %w", err)
	}

	if err := os.WriteFile(rawPath, data, 0644); err != nil {
		return fmt.Errorf("save raw file: %w", err)
	}

	// Parse FIT file from saved path
	activity, records, laps, zones, err := fitx.ParseFIT(rawPath)
	if err != nil {
		// Clean up file if parsing fails
		os.Remove(rawPath)
		return fmt.Errorf("parse FIT: %w", err)
	}

	// Database operations with transaction
	db := &store.DB{DB: s.db}
	return db.WithTx(func(tx *sql.Tx) error {
		// Check if activity already exists by FIT UID
		existingID, err := db.LookupActivityByUID(tx, activity.FitUID)
		if err == nil && existingID > 0 {
			// Clean up file if it's a duplicate
			os.Remove(rawPath)
			return fmt.Errorf("activity already exists with FIT UID: %s", activity.FitUID)
		}

		// Insert activity
		actID, err := db.InsertActivity(tx, activity, rawPath, hash)
		if err != nil {
			return fmt.Errorf("insert activity: %w", err)
		}

		// Insert records
		if err := db.InsertRecords(tx, actID, records); err != nil {
			return fmt.Errorf("insert records: %w", err)
		}

		// Insert laps
		if err := db.InsertLaps(tx, actID, laps); err != nil {
			return fmt.Errorf("insert laps: %w", err)
		}

		// Insert HR zones if available
		if len(zones) > 0 {
			var storeZones []store.HRZone
			for _, z := range zones {
				storeZones = append(storeZones, store.HRZone{
					Zone:        z.Zone,
					TimeSeconds: z.TimeSeconds,
				})
			}
			if err := db.InsertHRZones(tx, actID, storeZones); err != nil {
				return fmt.Errorf("insert hr zones: %w", err)
			}
		}

		// Update daily aggregations
		if err := db.UpsertDailyAgg(tx, activity.StartTimeUTC, activity); err != nil {
			return fmt.Errorf("upsert daily agg: %w", err)
		}

		return nil
	})
}

func writeUploadError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(uploadResponse{
		Success: false,
		Error:   message,
	})
}
