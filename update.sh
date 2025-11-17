#!/usr/bin/env bash
# Update this repo in-place and rebuild the Docker image.
# Env vars:
#   BRANCH   - branch to pull (default: current branch, fallback to main)
#   REMOTE   - git remote name (default: origin)
#   REPO_URL - optional URL override (uses remote.$REMOTE.url by default)
#   FORCE    - set to 1 to pull even with dirty working tree
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

REMOTE="${REMOTE:-origin}"
CUR_BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
BRANCH="${BRANCH:-${CUR_BRANCH:-main}}"

normalize_url() {
  local url="$1"
  if [[ "$url" =~ ^https?:// ]]; then
    printf '%s\n' "$url"
    return 0
  fi
  if [[ "$url" =~ ^git@([^:]+):(.+)$ ]]; then
    printf 'https://%s/%s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
    return 0
  fi
  return 1
}

RAW_URL="${REPO_URL:-$(git config --get remote."$REMOTE".url || true)}"
REPO_URL=""
if [[ -n "$RAW_URL" ]]; then
  if REPO_URL="$(normalize_url "$RAW_URL")"; then
    :
  else
    echo "Could not normalize remote URL '$RAW_URL'. Please set REPO_URL to an https URL." >&2
    exit 1
  fi
fi

if [[ -z "$REPO_URL" ]]; then
  echo "No REPO_URL provided and remote '$REMOTE' has no usable https URL. Aborting." >&2
  exit 1
fi

if [[ "${FORCE:-0}" != "1" ]]; then
  if ! git diff --quiet --ignore-submodules --cached || ! git diff --quiet --ignore-submodules; then
    echo "Working tree has uncommitted changes. Commit/clean or re-run with FORCE=1." >&2
    exit 1
  fi
fi

echo "Fetching $BRANCH from $REPO_URL ..."
git fetch "$REPO_URL" "$BRANCH"

echo "Checking out $BRANCH ..."
git checkout "$BRANCH"

echo "Pulling latest commits ..."
git pull --ff-only "$REPO_URL" "$BRANCH"

echo "Stopping any running stack ..."
docker compose down || true

echo "Rebuilding Docker image ..."
docker compose build

echo "Update complete. To restart:"
echo "  docker compose up -d"
