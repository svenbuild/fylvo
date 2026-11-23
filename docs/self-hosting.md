# Self-hosting Fylvo

This guide starts where the [quick start](../README.md#quick-start) ends: Fylvo runs from a folder
(here `~/fylvo`) with `docker-compose.yml`, and you created the admin account. The examples use
`cloud.example.com` for Fylvo and `office.example.com` for office; use your own names.

Settings go into a file `.env` next to `docker-compose.yml`. Start from the template:

```bash
cd ~/fylvo
curl -fsSL -o .env https://raw.githubusercontent.com/svenbuild/fylvo/main/.env.example
```

The template has every setting commented out; the examples below are lines you add to `.env` (or
uncomment there). After changing `.env`, apply it with `docker compose up -d`. Fylvo listens only on the server itself
(`127.0.0.1:5212`) until you change `FYLVO_BIND`. The commands use `docker compose`; put `sudo` in
front if your user is not in the `docker` group.

## 1. https with a reverse proxy

Open Fylvo to the internet only through https. The simplest way is [Caddy](https://caddyserver.com/docs/install),
which gets and renews the certificate by itself. Point the DNS name to your server and open the
ports 80 and 443.

`/etc/caddy/Caddyfile`:

```caddy
cloud.example.com {
	reverse_proxy 127.0.0.1:5212
}
```

Add to `.env`:

```bash
FYLVO_URL=https://cloud.example.com
FYLVO_TRUSTED_PROXIES=172.16.0.0/12
```

```bash
docker compose up -d && sudo systemctl reload caddy
```

- Port 5212 stays closed to the outside, only the proxy on the server reaches it.
- `FYLVO_TRUSTED_PROXIES` lets Fylvo see the real address of each visitor (the proxy reaches the
  container from the Docker network). Without it every visitor looks like the proxy, and wrong
  passwords from a stranger also lock you out for a while. Set it **only** while port 5212 is
  closed to the outside (the default): anyone who reaches 5212 directly comes from the same
  Docker network and could then fake their address.
- With another proxy (nginx, Traefik): forward to `127.0.0.1:5212`, pass `Host`,
  `X-Forwarded-For` and `X-Forwarded-Proto`, allow WebSockets, and do not limit the size of
  request bodies (uploads come in chunks, but WebDAV clients send whole files).

## 2. Office in the browser

Optional. [Collabora Online](https://www.collabora.com/) opens Word, Excel and PowerPoint files
in the browser. It runs as a second container and needs its own address, which browsers reach
directly. It uses about 500 MB of memory once a document was opened.

Caddyfile, next to the block for Fylvo:

```caddy
office.example.com {
	# Collabora converts any file posted to these paths. Close them to the outside.
	@tools path_regexp (?i)/(cool|lool)/+(convert-to|extract-link-targets|extract-document-structure|transform-document-structure|get-thumbnail|render-search-result)
	respond @tools 403
	reverse_proxy 127.0.0.1:9980
}
```

Add to `.env`, next to the lines from step 1:

```bash
FYLVO_OFFICE_URL=https://office.example.com
OFFICE_BEHIND_HTTPS=true
```

```bash
docker compose --profile office up -d && sudo systemctl reload caddy
```

Open a `.docx` file in Fylvo: it opens in the editor. From now on, use `--profile office` with
every `docker compose` command that should include office (`up`, `pull`).

Collabora must reach `FYLVO_URL` from inside its container, so the address has to resolve and be
reachable from the server itself. Without a proxy, in a home network, use the address of the
server for both and open the ports to your network: `FYLVO_URL=http://192.168.1.10:5212`,
`FYLVO_OFFICE_URL=http://192.168.1.10:9980`, `FYLVO_BIND=5212`, `OFFICE_BIND=9980`,
`OFFICE_BEHIND_HTTPS=false`, and no `FYLVO_TRUSTED_PROXIES` (see step 1).

## 3. Backup and restore

Everything Fylvo keeps is in the folder `~/fylvo`: your files and the database in `data/`, the
settings in `.env`. Back up the whole folder. Stop Fylvo for the copy, so the database is
consistent; it takes as long as copying your files.

```bash
bash -euc '
  cd ~/fylvo
  B=$HOME/fylvo-backup-$(date +%F-%H%M%S).tar.gz
  docker compose stop fylvo
  trap "docker compose start fylvo" EXIT
  sudo sh -c "umask 077 && tar -czf $B.part -C $HOME fylvo && mv $B.part $B"
  echo "Backup: $B"
'
```

Fylvo is started again in any case; the backup is only made when Fylvo stopped. Each run makes a
new file with date and time; a failed run leaves at most a `.part` file and reports an error. Run
one backup at a time. The archive
holds all your files and settings, so only root may read it (`umask 077`); copy it with `sudo`.

Keep the backup on another disk or machine. For large amounts of data, a tool that copies only
changes (`rsync`, `restic`, `borg`) is faster: stop Fylvo, copy the folder, start it again.

Restore a backup: the commands unpack it next to the current folder first and swap the two only
when that worked. They stop at the first error; until the swap your current Fylvo is untouched.
Unpacking needs free space for a second copy of your data.

```bash
B=$HOME/fylvo-backup-<date and time>.tar.gz
bash -euc '
  cd ~
  [ ! -e fylvo-old ] && [ ! -e fylvo-restore ] || { echo "Move ~/fylvo-old and ~/fylvo-restore away first."; exit 1; }
  mkdir fylvo-restore
  sudo tar -xzf "$1" -C fylvo-restore
  sudo test -d fylvo-restore/fylvo/data
  (cd fylvo && docker compose --profile office down)
  sudo mv fylvo fylvo-old
  sudo mv fylvo-restore/fylvo fylvo
  rmdir fylvo-restore
  cd fylvo && docker compose up -d
' restore "$B"
```

`--profile office` stops office too, if you use it; with office, start it again with
`docker compose --profile office up -d`. If your user is not in the `docker` group, write
`sudo docker compose` in the compose lines (also in the backup block).

On a new server, with Docker installed and no `~/fylvo` yet, unpacking is enough:
`B=~/fylvo-backup-<date and time>.tar.gz; sudo tar -xzf "$B" -C ~ && cd ~/fylvo && docker compose up -d`.
Set up the reverse proxy of step 1 again.

If an update is the problem, not the data: before `docker compose up -d`, set the image in
`docker-compose.yml` to the version that worked, for example `ghcr.io/svenbuild/fylvo:1.0.0`.

Sign in and open a few files. Delete `~/fylvo-old` when everything is there. A backup from an
older Fylvo version starts with the current image too; Fylvo updates the database by itself.

## 4. Update

Make a backup first (step 3), then:

```bash
cd ~/fylvo
docker compose pull
docker compose up -d
```

The image tag `1` in `docker-compose.yml` follows all 1.x releases. A new major version (2.x) is
announced in the [release notes](https://github.com/svenbuild/fylvo/releases) with the steps it
needs; you switch to it by changing the tag. With office, add `--profile office` to both commands.

## 5. Connect the apps

Download the apps from the [releases](https://github.com/svenbuild/fylvo/releases). When you sign
in, type the address of your server (`https://cloud.example.com`). The Android app only connects
over https.

| App | Install |
| --- | --- |
| Android | Open `Fylvo-Android-<version>.apk` on the phone and allow the installation. Android shows a warning because the app is not from the Play Store. |
| Windows | Unpack `Fylvo-Desktop-<version>-windows-x64.zip`, right-click `install.ps1`, "Run with PowerShell". The first time it asks for admin rights once, to trust the certificate the package is signed with. Then "Add drive" and sign in in the browser. |
| Linux (preview) | AppImage: make it executable and start it. `.deb`: `sudo apt install ./<file>.deb`. `.rpm`: `sudo dnf install ./<file>.rpm`. Then "Add drive". |

WebDAV works with any client: address `https://cloud.example.com/dav`, your email as user name and
a WebDAV account you create under "Connect & Mount" in the web app.

**App updates from your server.** The apps ask your server for new versions. Each release has a
file `fylvo-app-updates-<version>.tar.gz` with the new app packages in the layout Fylvo expects.
This replaces the app packages your server offers; if the download or the unpacking fails, the
old ones stay:

```bash
V=1.0.0   # the release
bash -euc '
  T=$(mktemp -d); trap "rm -rf $T" EXIT
  curl -fsSL -o $T/apps.tar.gz https://github.com/svenbuild/fylvo/releases/download/v$1/fylvo-app-updates-$1.tar.gz
  mkdir $T/new && tar --no-same-owner -xzf $T/apps.tar.gz -C $T/new
  cd ~/fylvo/data
  sudo rm -rf android.old desktop.old
  for d in android desktop; do
    [ -d $T/new/$d ] || continue
    [ ! -e $d ] || sudo mv $d $d.old
    sudo cp -r $T/new/$d $d
  done
  sudo rm -rf android.old desktop.old
' apps "$V"
```

Signed-in apps find the update within a few hours and install it after asking (Android) or when
nothing syncs (Windows, Linux). The apps only accept packages signed with the same key as the
installed app.

## 6. Google Drive and OneDrive

Optional. Users can connect their own Google Drive or OneDrive and see it as a folder next to
their files in Fylvo. For this, you register Fylvo once at each provider and enter the ID and
secret in the admin panel: [Google Drive](connect-google-drive.md),
[OneDrive](connect-onedrive.md). Both need Fylvo with https under a domain name (step 1).

Fylvo checks every connection to a provider and refuses internal addresses. If the server goes
out through a proxy (`HTTPS_PROXY`), the proxy decides which targets are reachable: Fylvo can
only check the address and its own name lookup, so the proxy itself has to block internal
addresses.
