# Garmr

A self-hosted fitness activity tracker for Garmin FIT files. Import, analyze, and visualize your fitness data with detailed charts, heart-rate zones, and training metrics.

## Quick Start

```bash
git clone https://github.com/kullbachxyz/garmr.git
cd garmr
# edit garmr.json → set "http_addr": "0.0.0.0:8765",
# and add a throwaway "auth_user"/"auth_pass" for the first login
docker compose up --build
```

Visit `http://localhost:8765`, log in with the seed credentials, then open **Account → Update password**. After the first user exists you can remove the bootstrap values.

## Config Cheatsheet

```json
{
  "db_path": "./data/garmr.db",
  "raw_store": "./data/raw_fit",
  "http_addr": "0.0.0.0:8765",
  "poll_ms": 0,
  "search_roots": ["/Volumes"],
  "garmin_dirs": ["/Volumes/GARMIN/GARMIN/Activity"],
  "use_cdn_tiles": false,
  "auth_user": "user",
  "auth_pass": "password"
}
```

 - `db_path` / `raw_store`: where SQLite and uploaded FIT files live (mount `/app/data` in Docker to persist them).
 - `http_addr`: `0.0.0.0:8765` for containers, `127.0.0.1:8765` for local dev.
 - `poll_ms`: enable background USB scans when running on your host OS (`0` disables; USB scanning isn’t available inside Docker).
- `search_roots` + `garmin_dirs`: paths to scan for devices.
- `auth_user` / `auth_pass`: bootstrap account only; the UI handles password changes afterwards.

Run with a custom file via `./garmrd -config ./my-config.json` or `docker run … garmr -config /path`.

## Local Development

```bash
git clone https://github.com/kullbachxyz/garmr.git
cd garmr
go build -o garmrd ./cmd/garmrd
./garmrd
```

You’ll need Go 1.21+ and SQLite headers (the repo already vendors modernc.org/sqlite). To mirror production, the provided `Dockerfile` builds the binary with Go 1.25 and packages it in a slim Debian image:

```bash
docker build -t garmr .
docker run --rm -p 8765:8765 -v garmr_data:/app/data -v $PWD/garmr.json:/app/garmr.json:ro garmr
```

## Everyday Use

1. Start the server (binary or Docker).
2. Browse to `http://localhost:8765`.
3. Import FIT files via USB scan or drag-and-drop uploads.
4. Review distance, pace, heart-rate zones, elevation, and training-effect charts.
