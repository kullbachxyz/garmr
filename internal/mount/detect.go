package mount

import (
	"os"
	"path/filepath"
)

// FindGarminActivityDirs scans roots for GARMIN activity directories.
func FindGarminActivityDirs(roots, garminDirs []string) []string {
	var hits []string
	for _, r := range roots {
		entries, _ := os.ReadDir(r)
		for _, e := range entries {
			p := filepath.Join(r, e.Name())
			if !e.IsDir() { continue }
			for _, g := range garminDirs {
				cand := filepath.Join(p, g)
				if _, err := os.Stat(cand); err == nil {
					hits = append(hits, cand)
				}
		        // also try with the volume name itself (some devices mount as /Volumes/GARMIN then have Activity at that level)
		        alt := filepath.Join(r, g) // e.g., /Volumes/GARMIN/Activity
		        if _, err := os.Stat(alt); err == nil {
		          hits = append(hits, alt)
		        }
			}
		}
	}
	return hits
}
