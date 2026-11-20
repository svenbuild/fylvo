# Fylvo for Android

Native Android client for Fylvo (Kotlin, Jetpack Compose, Material 3).

## Origin and license

This app is a modified copy of the Android client of the community fork
[Dvorinka/cloudreve](https://github.com/Dvorinka/cloudreve), taken from its
`android/` folder at commit `86853e5103a3200635b14c7d3b72b11277e45e17`.
The original app was written by Tomas Dvorak and Dixit Sabharwal and is
licensed under the GNU General Public License v3.0. This copy stays under the
same license, see [LICENSE](LICENSE). The first commit of this folder is the
unchanged import; every Fylvo change is in the commits after it.

Fylvo itself is a fork of [Cloudreve](https://github.com/cloudreve/cloudreve).
Neither project is affiliated with or endorses Fylvo.

## What Fylvo changed

- Own name, application ID and package `io.github.svenbuild.fylvo.android`, Fylvo
  icon and colors, the Inter font of the web app (SIL Open Font License, see
  [FONT-LICENSE-Inter.txt](FONT-LICENSE-Inter.txt)), a floating bottom bar
  with Home, Files, Shared and Account (like Google Drive, OneDrive and
  Dropbox; no photo tab, photos stay in Immich). Offline files sit under
  Account and carry a pin in the lists.
- Access and refresh tokens are encrypted with a key held in the Android
  Keystore. Backups and device transfers exclude all app data.
- No plain `http://`. Release builds only connect over https with the
  system certificate authorities. Debug builds allow plain http to two test
  addresses only, see `app/src/debug/res/xml/network_security_config.xml`.
- No photo backup and no photo or media permissions. Photos stay in Immich.
- No Google Play update check, no browser (OAuth) sign-in, no
  `cloudreve://` links from other apps. The app talks only to the server
  address the user enters.
- The bearer token goes only to that server's exact origin. No HTTP logging.
- Sign-out also stops background jobs and deletes offline copies, cached
  downloads and the folder-sync and notification settings.
- In-app updates from the own Fylvo server instead of a store: the app asks
  `api/v4/fylvo/android/latest` once per start and on "App version" under
  Account, downloads the APK, checks size, SHA-256, package name, version and
  that it is signed with the same key, then opens the system installer after
  a tap. Needs the `REQUEST_INSTALL_PACKAGES` permission (Android asks the user
  once to allow installs from Fylvo).
- "Shared with me": folders other Fylvo users share with you. Inside a
  shared folder the app offers only what the server's capability set allows
  (mirrors `frontend/src/util/permission.ts`).

## Build

Needs JDK 17 and an Android SDK with platform 36.

```bash
cd android
./gradlew :app:assembleDebug        # app/build/outputs/apk/debug/app-debug.apk
./gradlew :app:testDebugUnitTest
```

Release builds are signed with the Fylvo key when both inputs exist,
otherwise `assembleRelease` produces an unsigned APK (never the debug key):

- Gradle property `fylvoKeystore` with the path of the keystore, for example
  in `~/.gradle/gradle.properties` (outside the repo).
- Environment variable `FYLVO_KEYSTORE_PASSWORD` at build time.

```bash
FYLVO_KEYSTORE_PASSWORD=... ./gradlew :app:assembleRelease   # app/build/outputs/apk/release/app-release.apk
```

Never commit a keystore or its password. Losing either means updates no
longer install over the existing app.
