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

## Screenshots

The web interface provides:
- Dashboard with weekly, monthly, and yearly statistics
- Detailed activity views with interactive maps and charts
- Heart rate zone visualization with percentages
- Training effect metrics for supported devices

## Installation

### Prerequisites

- Go 1.21 or later
- Git

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

## Usage

1. Start the application: `./garmrd`
2. Open your browser to `http://localhost:8765`
3. Import activities:
   - **USB Import**: Connect your Garmin device and use the "Scan & Import from USB" button
   - **File Upload**: Drag and drop FIT files or use the file picker

## Data Storage

- **Database**: SQLite database stores activity metadata, records, and statistics
- **Raw Files**: Original FIT files are preserved in the raw storage directory
- **No Cloud**: All data stays on your local system

## Supported Devices

Any Garmin device that produces standard FIT files. Tested with:
- Garmin watches (Forerunner, Fenix, etc.)
- Garmin Edge cycling computers
- Other ANT+ and FIT-compatible devices

## Development

```bash
# Install dependencies
go mod tidy

# Run with live reload (if using air)
air

# Run tests
go test ./...
```

## Architecture

- **Backend**: Go with SQLite database
- **Frontend**: HTML templates with Chart.js for visualizations
- **Mapping**: Leaflet with OpenStreetMap tiles
- **FIT Parsing**: github.com/tormoder/fit library

## License

MIT License - see LICENSE file for details

## Contributing

Contributions welcome! Please read the contributing guidelines and submit pull requests.

## Support

For issues and feature requests, please use the GitHub issue tracker.