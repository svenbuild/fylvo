# Fylvo Desktop unter Linux

Fylvo Desktop gleicht Ordner auf dem Linux-Rechner mit deinem Fylvo-Server ab. Anders als unter
Windows liegen alle Dateien ganz auf dem Rechner (ein normaler Ordner), damit jedes Programm sie
öffnen kann, auch ohne Netz. Technischer Plan: `docs/linux-plan.md`.

## Welches Paket?

| Paket | Für | Updates |
| --- | --- | --- |
| `Fylvo Desktop_<Version>_amd64.AppImage` | jedes Linux (eine einzelne Datei) | still, die App ersetzt sich selbst |
| `Fylvo Desktop-<Version>-1.x86_64.rpm` | Fedora, openSUSE | die App installiert, das System fragt nach dem Admin-Passwort |
| `Fylvo Desktop_<Version>_amd64.deb` | Ubuntu, Debian, Mint | wie rpm |

Empfehlung für Fedora: das **.rpm** (erscheint im Startmenü, `fylvo://`-Links gehen sofort).

## Installieren

**rpm (Fedora):** Terminal öffnen und
```
sudo dnf install "./Fylvo Desktop-0.3.0-1.x86_64.rpm"
```
**deb (Ubuntu, Debian):**
```
sudo apt install "./Fylvo Desktop_0.3.0_amd64.deb"
```
**AppImage:** Datei z.B. nach `~/Programme` legen, ausführbar machen (`chmod +x`, oder im
Dateimanager unter Eigenschaften), doppelklicken. Auf Ubuntu braucht ein AppImage einmalig
`sudo apt install libfuse2`. Die Datei muss in einem Ordner liegen, in den du schreiben darfst
(sonst gehen Updates nicht).

Danach Fylvo Desktop starten, "Laufwerk hinzufügen", im Browser bei Fylvo anmelden, einen
**leeren** Ordner wählen (z.B. `~/Fylvo`) und den Cloud-Ordner, der dort hin soll.

## Gut zu wissen

- **Tray-Symbol unter GNOME (Fedora):** GNOME zeigt ohne Erweiterung keine Tray-Symbole.
  Erweiterung "AppIndicator and KStatusNotifierItem Support" installieren
  (`sudo dnf install gnome-shell-extension-appindicator`, abmelden, anmelden). Ohne sie öffnet ein
  erneuter Start von Fylvo Desktop aus dem Startmenü das Fenster.
- **Rechtsklick "Fylvo: Online ansehen":** in GNOME Files unter "Skripte", in Dolphin direkt im
  Menü. Die App legt die Einträge beim Start an.
- **Autostart:** in den Einstellungen der App (legt `~/.config/autostart/fylvo-desktop.desktop` an).
- **Erlaubte Laufwerke:** ext4, btrfs, xfs, f2fs. USB-Sticks mit FAT/exFAT und Netzwerkfreigaben
  werden abgelehnt (dort bleiben die Merkmale nicht stabil, an denen die App Änderungen erkennt).
- **Sehr viele Unterordner:** Linux erlaubt einem Programm nur eine bestimmte Zahl beobachteter
  Ordner. Meldet die App "Nicht alle Änderungen werden bemerkt":
  `echo fs.inotify.max_user_watches=524288 | sudo tee /etc/sysctl.d/90-fylvo.conf && sudo sysctl --system`
- **Symlinks** und eingebundene Laufwerke im Sync-Ordner werden nie abgeglichen.
- **Papierkorb:** vom Server Gelöschtes landet im Linux-Papierkorb, hier Gelöschtes im
  Papierkorb des Servers. Ab 50 Löschungen (oder 10, wenn es 30 % sind) fragt die App nach.
- **Verschieben zwischen zwei Sync-Ordnern** wird rückgängig gemacht (wie unter Windows). Kopieren
  geht.

## Bekannte Grenzen

- Läuft die App nicht, während du eine Datei löschst oder umbenennst, kommt beim nächsten Start
  die alte Datei vom Server zurück (Umbenennen: dann gibt es beide Namen). Wie unter Windows.
- Ein Programm, das eine Datei an Ort und Stelle überschreibt und dabei Grösse und Änderungszeit
  gleich lässt, wird nicht bemerkt. Wie unter Windows.
- Ein ganzer Ordner, der in einen eben erst angelegten Ordner verschoben wird, wird am Server
  gelöscht (Papierkorb) und neu hochgeladen; Freigaben und Versionen bleiben beim alten. Bei
  einzelnen Dateien wird die Verschiebung erkannt.
- Zwischen der Prüfung "ist es noch dieselbe Datei am Server" und dem Löschen am Server liegen
  Millisekunden; der Server kennt kein bedingtes Löschen.

## Updates veröffentlichen (vom PC)

Pakete bauen in der WSL-Distribution `FylvoBuild` (`~/fylvo/desktop/src-tauri`, `cargo tauri build`).
Veröffentlichen mit Signatur (Schlüssel ohne Passwort unter
`Documents\Projects\Fylvo-Signing\fylvo-desktop-linux-release.pem`, gehört nie ins Repo):
```
scripts/publish-desktop.sh "Fylvo Desktop_0.3.1_amd64.AppImage" linux-x64-appimage
scripts/publish-desktop.sh "Fylvo Desktop-0.3.1-1.x86_64.rpm" linux-x64-rpm
scripts/publish-desktop.sh "Fylvo Desktop_0.3.1_amd64.deb" linux-x64-deb
```
Das Skript läuft unter Linux oder Git Bash (braucht `openssl` 3); `FYLVO_LINUX_KEY` zeigt auf den
Schlüssel. Die Version steht in `src-tauri/tauri.conf.json`. Die App fragt 2 Minuten nach dem
Start und dann alle 6 Stunden, installiert nur, wenn kein Abgleich läuft, und meldet sich nach
dem Neustart mit "Aktualisiert".
