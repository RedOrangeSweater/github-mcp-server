## Docker images for this fork (GHCR)

This repository publishes Docker images to **GitHub Container Registry**.

- **Image**: `ghcr.io/redorangesweater/github-mcp-server`
- **Common tags**:
  - **`main`**: latest build from `main`
  - **`sha-<...>`**: immutable build tag for a specific commit
  - **`vX.Y.Z`**: release tag builds
  - **`latest`**: only for stable `vX.Y.Z` tags (no pre-releases)

### Pull

```bash
docker pull ghcr.io/redorangesweater/github-mcp-server:main
```

### Run

```bash
docker run -i --rm \
  -e GITHUB_PERSONAL_ACCESS_TOKEN=... \
  ghcr.io/redorangesweater/github-mcp-server:main
```

### Notes

- If you see `denied` errors while pulling, run `docker logout ghcr.io` and try again (stale credentials can override public access).
- Package visibility is controlled under GitHub **Packages** settings for the org/repo.
