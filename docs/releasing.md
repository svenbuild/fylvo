# Releasing

For the maintainer. Server versions follow [semantic versioning](https://semver.org): `v1.0.1`
fixes, `v1.1.0` new features, `v2.0.0` changes that need steps from the admin. The apps keep their
own version numbers. The signing keys of the apps stay on the maintainer's PC, never in the
repository or the CI.

## 1. Server and image

```bash
git switch main && git pull
git tag -a v1.0.0 -m "Fylvo 1.0.0"
git push origin v1.0.0
```

The workflow [Release](../.github/workflows/release.yml) builds and tests the server, pushes
`ghcr.io/svenbuild/fylvo:1.0.0` and creates a **draft** release with the server archives. When you
publish the release, [Promote](../.github/workflows/promote.yml) also tags that image `:1.0`, `:1`
and `:latest`, the tags people pull. A tag like `v1.1.0-rc.1` becomes a pre-release and is never
promoted.

Publish one release at a time and wait until Promote has finished before the next one: GitHub keeps
only one waiting run of it.

The very first time, try the pipeline with a pre-release tag (`v1.0.0-rc.1`, never promoted) while
the repository is still private, then delete that release, its image version and the tag
(`git push --delete origin v1.0.0-rc.1 && git tag -d v1.0.0-rc.1`).

Only the first time: GitHub creates the image as private. Open the package `fylvo` under the
profile's "Packages", "Package settings", set the visibility to public and link it to the
repository.

## 2. Apps

Build the signed apps: Android as described in [android/README.md](../android/README.md)
(`FYLVO_KEYSTORE_PASSWORD=... ./gradlew :app:assembleRelease`), Windows with
`.\build-msix.ps1 -Arch x64 -SignPasswordFile <file with the password>` (and `-SignPfx <path>` when
the certificate is not in the default place), Linux as in [desktop/LINUX.md](../desktop/LINUX.md).
Then put them into one folder per release, here
`~/fylvo-release/1.0.0`:

| Put into `~/fylvo-release/1.0.0` | From |
| --- | --- |
| `Fylvo-Android-1.4.0.apk` | `android/app/build/outputs/apk/release/app-release.apk`, renamed |
| `Fylvo.x64.msix` | `desktop/dist/Fylvo.x64.msix` |
| `Fylvo-Desktop-0.3.0-windows-x64.zip` | a zip of `Fylvo.x64.msix`, `desktop/install.ps1` and `desktop/package/fylvo-desktop.cer` |
| AppImage, `.deb`, `.rpm` | `desktop/target/release/bundle/` of the Linux build |

The update bundle for self-hosted servers uses the same checks as publishing to a server. Run it
in Git Bash on Windows (the MSIX check needs Windows PowerShell and a PC that trusts the Fylvo
certificate), from the repository folder, with `ANDROID_HOME` set and `FYLVO_LINUX_KEY` pointing to
the Linux release key:

```bash
R=~/fylvo-release/1.0.0
bash -euc '
  R="$1"; B="$(mktemp -d)"
  FYLVO_PUBLISH_LOCAL=$B/android bash scripts/publish-android.sh "$R/Fylvo-Android-1.4.0.apk"
  FYLVO_PUBLISH_LOCAL=$B/desktop bash scripts/publish-desktop.sh "$R/Fylvo.x64.msix" windows-x64
  FYLVO_PUBLISH_LOCAL=$B/desktop bash scripts/publish-desktop.sh "$R/Fylvo Desktop_0.3.0_amd64.AppImage" linux-x64-appimage
  FYLVO_PUBLISH_LOCAL=$B/desktop bash scripts/publish-desktop.sh "$R/Fylvo Desktop_0.3.0_amd64.deb" linux-x64-deb
  FYLVO_PUBLISH_LOCAL=$B/desktop bash scripts/publish-desktop.sh "$R/Fylvo Desktop-0.3.0-1.x86_64.rpm" linux-x64-rpm
  # Owner root, no user names in the archive.
  tar -C "$B" --exclude=.publish.lock --owner=0 --group=0 --numeric-owner -czf "$R/fylvo-app-updates-1.0.0.tar.gz" android desktop
  tar -tvzf "$R/fylvo-app-updates-1.0.0.tar.gz"
  rm -rf "$B"
' bundle "$R"
```

It stops at the first failed check and starts from an empty folder each time, so a retry never
packs older files. The listing at the end must show `0/0` as owner on every line.

## 3. Publish

```bash
cd ~/fylvo-release/1.0.0
gh release upload v1.0.0 --repo svenbuild/fylvo Fylvo-Android-*.apk Fylvo-Desktop-*.zip *.AppImage *.deb *.rpm fylvo-app-updates-*.tar.gz
```

Open the draft on GitHub, fill in "Changes" and the app versions, then "Publish release". Then
check that a plain `docker pull ghcr.io/svenbuild/fylvo:1` works without being logged in.
