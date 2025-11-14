# Garmr

A self-hosted fitness activity tracker for Garmin FIT files. Import, analyze, and visualize your fitness data with detailed charts, heart rate zones, and training metrics.

## Features

- **Activity Import**: Import FIT files from USB-connected Garmin devices or via web upload
- **Rich Visualizations**: Interactive charts for elevation, pace, and heart rate data
- **Heart Rate Analysis**: 5-zone heart rate analysis with time-in-zone breakdowns
- **Training Metrics**: Aerobic and anaerobic training effect tracking (Garmin devices)
- **Multi-Sport Support**: Track running, cycling, hiking, and other activities
- **Responsive Design**: Works on desktop and mobile devices
- **Statistics Dashboard**: Aggregated stats with sport filtering

## Installation

### Prerequisites

- Go 1.21 or later
- Git
- Docker (optional, for containerized deploys)

### Building from Source

```bash
# Clone the repository
git clone https://github.com/yourusername/garmr.git
cd garmr

# Build the binary
go build -trimpath -ldflags="-s -w" -o garmrd ./cmd/garmrd

# Run the application
./garmrd
```

### Configuration

Create a `garmr.json` configuration file:

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

The application uses `garmr.json` by default:

```bash
# Uses ./garmr.json automatically
./garmrd

# Or specify a custom config file
./garmrd -config ./my-config.json
```

**Configuration Options:**
- `db_path`: SQLite database file location
- `raw_store`: Directory to store original FIT files
- `http_addr`: HTTP server address and port
- `poll_ms`: Background USB polling interval (0 to disable)
- `search_roots`: Root paths to search for Garmin devices
- `garmin_dirs`: Subdirectories within Garmin devices containing activities
- `use_cdn_tiles`: Use CDN for map tiles (true) or self-host (false)

## Docker

Build and run the containerized server:

```bash
docker build -t garmr .

docker run --rm \
  -p 8765:8765 \
  -v garmr_data:/app/data \
  -v $PWD/garmr.json:/app/garmr.json:ro \
  garmr
```

Set `http_addr` to `0.0.0.0:8765` (or override via `-config`) so the UI is reachable outside the container. When using Docker Compose, mount `/app/data` to persist the SQLite database and raw FIT files:

```yaml
services:
  garmr:
    build: .
    ports:
      - "8765:8765"
    volumes:
      - garmr_data:/app/data
      - ./garmr.json:/app/garmr.json:ro
volumes:
  garmr_data: {}
```

## Usage

1. Start the application: `./garmrd`
2. Open your browser to `http://localhost:8765`
3. Import activities:
   - **USB Import**: Connect your Garmin device and use the "Scan & Import from USB" button
   - **File Upload**: Drag and drop FIT files or use the file picker

## Supported Devices

Any Garmin device that produces standard FIT files. Tested with:
- Garmin watches (Forerunner, Fenix, etc.)
- Garmin Edge cycling computers
- Other ANT+ and FIT-compatible devices

## Architecture

- **Backend**: Go with SQLite database
- **Frontend**: HTML templates with Chart.js for visualizations
- **Mapping**: Leaflet with OpenStreetMap tiles
- **FIT Parsing**: github.com/tormoder/fit library
