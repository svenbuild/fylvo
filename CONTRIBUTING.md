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
scripts/build.sh                             # on an arm64 machine: FYLVO_ARCH=arm64 scripts/build.sh
docker build -t fylvo:dev .
```

For both architectures at once, as the release workflow does:

```bash
FYLVO_ARCH="amd64 arm64" scripts/build.sh
docker buildx build --platform linux/amd64,linux/arm64 -t <registry>/fylvo:dev --push .
```

## Connected storages without an account at a provider

To work on connected storages (Google Drive, OneDrive) in the web app or a phone app, the
server can play the provider itself:

```bash
go build -tags fylvo_dev -o fylvo-dev .
FYLVO_DEV_STORAGE=1 ./fylvo-dev
```

Both are needed: the code is only part of a build with that tag, and it only does something
with that variable. An ordinary build contains none of it. The log says so at the first use.

- "Google Drive" is then set up and leads to a storage with sample data (folders, files,
  pictures, Google documents, two entries with one name, shortcuts, a folder with 250 files).
  The data lives in memory and is the same after every start. OneDrive stays "not set up".
- `FYLVO_DEV_STORAGE_FILES=50000` adds a folder with that many files, to try very large folders.
- `FYLVO_CONNECTED_WRITE=1` lets the server change connected storages (upload, create, rename,
  move, delete, save), as on any installation. Without it they are read-only.
- Connecting works as with a real provider, but the sign-in page is one of the server:
  "Allow" as one of two sample accounts, or "Deny".
- Connecting from an app (`client: "app"`) works here over plain http too, so it can be tried
  on an emulator. An ordinary server offers it only with https.
- The sample pictures have previews, as pictures have at a real provider.
- `http://<your server>/api/v4/fylvo/dev/expire` ends all access, so every sample storage
  has to be connected again.
- The browser has to reach the server under its first site address (admin area, "Site URL"):
  `http://localhost:5212` for the web app on the same machine, the address of the machine as
  the emulator sees it for a phone app.

Never use such a build for a server with real data: the sign-in page lets anyone who can
reach it choose the account. The simulated provider itself stores nothing, but the storages
you connect to it are ordinary rows of the database: disconnect them, or use a data folder of
its own, before an ordinary build runs on the same data.

## Pull requests

- One change per pull request, with a short description of what and why.
- Tests for new logic; `go test` and `npx tsc --noEmit` in `frontend/` pass.
- Code and comments in English, simple and short.
