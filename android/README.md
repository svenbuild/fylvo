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
  icon and colors.
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

Release signing is not set up yet: `assembleRelease` produces an unsigned
APK. Never commit a keystore or its passwords.
