# Garmr

A self-hosted fitness activity tracker for Garmin FIT files. Import, analyze, and visualize your fitness data with detailed charts, heart-rate zones, and training metrics.

## Quick Start (Docker Compose)

   ```bash
   git clone https://github.com/kullbachxyz/garmr.git
   cd garmr
   docker compose up --build
   ```

## Features

- **Activity Import**: USB scanning plus manual FIT upload.
- **Rich Visualizations**: Elevation, pace, heart-rate charts, and zone breakdowns.
- **Training Metrics**: Aerobic/anaerobic training effect summaries.
- **Multi-Sport**: Running, cycling, hiking, and other FIT-compatible workouts.
- **Responsive UI**: Works on desktop and mobile browsers.

## Configuration

The server reads `garmr.json` by default:

```json
{
  "db_path": "./data/garmr.db",
  "raw_store": "./data/raw_fit",
  "http_addr": "127.0.0.1:8765",
  "poll_ms": 0,
  "search_roots": ["/Volumes"],
  "garmin_dirs": ["/Volumes/GARMIN/GARMIN/Activity"],
  "use_cdn_tiles": false
}
```

Key fields:
- `db_path`: SQLite database file.
- `raw_store`: Folder for original FIT uploads.
- `http_addr`: HTTP bind address (set to `0.0.0.0:8765` inside Docker).
- `poll_ms`: USB polling interval in milliseconds (0 disables polling).
- `search_roots` / `garmin_dirs`: Candidate paths for attached Garmin storage.
- `use_cdn_tiles`: `true` to load map tiles from a CDN, `false` to self-host.

Override the config path with `./garmrd -config ./my-config.json` or pass `-config` through Docker (`docker run garmr -config /path`).

## Local Development (Go)

Prerequisites: Go 1.21+ and Git.

```bash
git clone https://github.com/yourusername/garmr.git
cd garmr

go build -trimpath -ldflags="-s -w" -o garmrd ./cmd/garmrd
./garmrd
```

The multi-stage `Dockerfile` in the repo builds the same binary with Go 1.25.1:

```bash
docker build -t garmr .
docker run --rm -p 8765:8765 -v garmr_data:/app/data -v $PWD/garmr.json:/app/garmr.json:ro garmr
```

## Usage

1. Start the server (locally or via Docker).
2. Open `http://localhost:8765`.
3. Import activities via USB scan or FIT upload and review charts, heart-rate zones, and training metrics.

## Architecture & Tech Stack

- **Backend**: Go + SQLite (migrations via Goose in `internal/store/migrations`).
- **FIT Parsing**: [`github.com/tormoder/fit`](https://github.com/tormoder/fit).
- **Frontend**: HTML templates with Chart.js visualizations and Leaflet + OSM tiles.
