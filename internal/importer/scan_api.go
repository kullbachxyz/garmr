package importer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"garmr/internal/importlog"
)

// ScanSummary is returned to the web UI after a manual import.
type ScanSummary struct {
	Roots      []string `json:"roots"`
	Dirs       []string `json:"dirs"`
	FoundFiles int      `json:"found_files"`
	Imported   int      `json:"imported"`
	Duplicates int      `json:"duplicates"`
	Errors     []string `json:"errors"`
}

// ScanOnce scans the configured Garmin activity directories once and ingests any .fit files.
// It’s designed to be called by the /api/import handler.
func (im *Importer) ScanOnce() (ScanSummary, error) {
	sum := ScanSummary{
		Roots: im.c.SearchRoots,
	}

	// 1) Resolve candidate directories
	var dirs []string
	// Use explicit GarminDirs if provided
	for _, d := range im.c.GarminDirs {
		if d == "" {
			continue
		}
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			dirs = append(dirs, d)
		}
	}

	// If none configured/found, try some common defaults under the search roots
	if len(dirs) == 0 {
		for _, root := range im.c.SearchRoots {
			if root == "" {
				continue
			}
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() {
					return nil
				}
				// quick name & path check
				if strings.EqualFold(d.Name(), "Activity") &&
					strings.Contains(strings.ToUpper(path), "GARMIN/GARMIN") {
					dirs = append(dirs, path)
					return filepath.SkipDir
				}
				return nil
			})
		}
	}

	if len(dirs) == 0 {
		importlog.Printf("import: no activity dirs found (search_roots=%v, garmin_dirs=%v)", im.c.SearchRoots, im.c.GarminDirs)
		return sum, nil
	}
	sum.Dirs = dirs

	// 2) Find .fit files (case-insensitive)
	var files []string
	pats := []string{"*.fit", "*.FIT", "*.Fit"}
	for _, d := range dirs {
		for _, p := range pats {
			glob := filepath.Join(d, p)
			if matches, _ := filepath.Glob(glob); len(matches) > 0 {
				files = append(files, matches...)
			}
		}
	}
	sum.FoundFiles = len(files)
	importlog.Printf("import: %d dir(s) -> %d file(s)", len(dirs), len(files))
	if len(files) == 0 {
		return sum, nil
	}

	// 3) Ingest files
	for _, f := range files {
		if err := IngestFile(im.db, im.c.RawStore, f); err != nil {
			// Check for duplicate error
			if errors.Is(err, ErrDuplicate) {
				sum.Duplicates++
				importlog.Printf("ingest: %s -> duplicate (skipped)", f)
				continue
			}
			sum.Errors = append(sum.Errors, fmt.Sprintf("%s: %v", f, err))
			importlog.Printf("ingest: %s -> ERROR: %v", f, err)
			continue
		}
		sum.Imported++
		importlog.Printf("ingest: %s -> imported", f)
	}

	return sum, nil
}
