package cfg

import (
	"encoding/json"
	"log"
	"os"
)

type Config struct {
	DBPath      string   `json:"db_path"`
	RawStore    string   `json:"raw_store"`
	HTTPAddr    string   `json:"http_addr"`
	PollMs      int      `json:"poll_ms"`
	SearchRoots []string `json:"search_roots"`
	GarminDirs  []string `json:"garmin_dirs"`
	UseCDNTiles bool     `json:"use_cdn_tiles"`
	AuthUser    string   `json:"auth_user"`
	AuthPass    string   `json:"auth_pass"`
}

func Default() Config {
	return Config{
		DBPath:      "./data/garmr.db",
		RawStore:    "./data/raw_fit",
		HTTPAddr:    "127.0.0.1:8765",
		PollMs:      0,
		SearchRoots: []string{"/Volumes", "/media", "/run/media", "D:/"},
		GarminDirs: []string{
			"GARMIN/Activity",   // most common on newer devices
			"GARMIN/Activities", // some units
			"GARMIN/ACTIVITY",   // legacy uppercase
			"Garmin/Activity",   // case variants (mac usually case-insensitive, but safe)
			"Garmin/Activities",
		},
		UseCDNTiles: true,
		AuthUser:    "",
		AuthPass:    "",
	}
}

func Load(path string) Config {
	c := Default()
	f, err := os.Open(path)
	if err != nil {
		log.Printf("config: using defaults (%v)", err)
		return c
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		log.Printf("config decode: %v (using defaults)", err)
		return Default()
	}
	return c
}
