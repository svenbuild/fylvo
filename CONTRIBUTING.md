# Contributing

Fylvo is a hobby project. Bug reports and ideas are welcome as
[issues](https://github.com/svenbuild/fylvo/issues). For a larger change, open an issue first so
we can agree on it before you spend time on a pull request.

## Layout

| Folder | What it is | Built with |
| --- | --- | --- |
| `/` | Server (Go) | Go, see `go.mod` |
| `frontend/` | Web app (React, TypeScript, Vite), embedded into the server | Node.js 22, Yarn 1 |
| `desktop/` | Windows and Linux app (Tauri, Rust, React) | see [desktop/README.md](desktop/README.md) |
| `android/` | Android app (Kotlin, Jetpack Compose) | see [android/README.md](android/README.md) |
| `docs/` | Guides and README images | |

## Server and web app

```bash
scripts/build.sh                             # web app + release/fylvo-linux-amd64
FYLVO_ARCH="amd64 arm64" scripts/build.sh    # several architectures
scripts/test.sh                              # the Go tests the CI runs
cd frontend && yarn install && yarn dev      # web app with hot reload against a server on :5212
```

Run a build with `./release/fylvo-linux-amd64`; it creates `data/` next to the binary. The first
account you register becomes the admin.

The Docker image is built from the binaries in `release/`:

```bash
scripts/build.sh
docker build -t fylvo:dev .
```

For both architectures at once, as the release workflow does:

```bash
FYLVO_ARCH="amd64 arm64" scripts/build.sh
docker buildx build --platform linux/amd64,linux/arm64 -t <registry>/fylvo:dev --push .
```

## Pull requests

- One change per pull request, with a short description of what and why.
- Tests for new logic; `go test` and `npx tsc --noEmit` in `frontend/` pass.
- Code and comments in English, simple and short.
