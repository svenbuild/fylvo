# Fylvo Android: mehr Funktionen der Web-App (Version 1.6.0)

Stand 2026-11-23. Ziel: die Handy-App kann im Alltag das meiste, was die Web-App kann. Gebaut wird
nur in der App (`android/`); Server und Web-App bleiben, wie sie sind. Eine Lieferung mit Paket 1
und Paket 2, Version 1.6.0 (versionCode 12).

## Grundsätze

- **Bestehende Wege der App.** Neue Funktionen kommen in die vorhandenen Orte: Aktionsblatt einer
  Datei (`FileActionSheet`), Auswahl-Menü (`SelectionActions`), Blatt "+" (Hochladen, neuer Ordner),
  Details (`DetailsSheet`), Seiten über der Liste wie "Offline" und "Anpassen" (gleiche Animation,
  Reiterleiste bleibt), Seite "Konto". Keine neue Navigation, keine neuen Reiter.
- **Nur anbieten, was erlaubt ist.** Was die Gruppe nicht darf (Bits der Gruppe, siehe unten) oder
  was der Datei fehlt (Fähigkeits-Bits der Datei, Besitz, Ort), wird nicht gezeigt. Der Server
  prüft zusätzlich; seine Fehlercodes bekommen eigene, verständliche Sätze (`util/ErrorText.kt`).
- **Verbundene Speicher:** nur was ihre Bits erlauben. Teilen, Versionen, Verlauf, Archive,
  Mit-Personen-Teilen, neue Textdatei und Ordner-Upload gibt es dort nicht (wie im Web).
- **Sicherheit:** Passwörter, 2FA-Codes und das 2FA-Geheimnis liegen nur in `remember` (nie
  `rememberSaveable`, nie DataStore, nie im Log); das Blatt mit dem 2FA-Geheimnis setzt
  `FLAG_SECURE`. Ein Freigabe-Link wird nur angenommen, wenn Schema, Host und Port genau zum
  angemeldeten Server passen; sonst geht keine Anfrage hinaus.
- **Texte** Deutsch (Schweizer Schreibweise, kein "ß") und Englisch.

## Grundlage (Etappe 0)

- `GET /api/v4/user/me` einmal pro Sitzung (und beim Öffnen von Konto): `id`, `nickname`, `avatar`,
  `group.permission` (Base64-Bitsatz). Gehalten im Speicher bei `SessionManager`/einem kleinen
  `AccountRepository`, mit der Sitzungsmarke (`SessionTag`) wie die Anpassungen. Fehlt die Antwort,
  gelten die Gruppen-Rechte als unbekannt: dann bietet die App an, was sie heute anbietet (Teilen),
  und neue, rechtepflichtige Dinge (Archive) erst nach der Antwort.
- Gruppen-Bits (wie `frontend/src/api/user.ts`): 2 Teilen, 5 Archiv-Aufgaben. Datei-Bits (wie heute
  `Permissions`): 1 umbenennen, 6 hochladen, 7 herunterladen, 18 teilen, 20 Versionen.
- `GET /site/config/explorer` liest zusätzlich `map_provider`, `full_text_search`, `file_viewers`
  (für Office: Endungen der eingeschalteten `wopi`-Betrachter, gefiltert nach `platform` und
  `required_group_permission` wie `siteConfigSlice.ts`).

## Paket 1

### 1. Eigene Freigaben verwalten

- **Wo:** im Reiter "Geteilt" stehen oben zwei Chips "Mit mir geteilt" und "Von mir geteilt" (wie die
  Orte-Chips über "Meine Dateien"). "Von mir geteilt" öffnet die Seite **Freigaben** (Seite wie
  Favoriten, `PAGE_SHARES`). Dieselbe Seite auch unter Konto ("Meine Freigaben"), weil der Reiter
  "Geteilt" abgewählt sein kann.
- **Seite:** `GET /share?page_size=50&order_direction=desc`, weiter mit `next_token`. Zeile: Symbol
  nach Typ, Name, eine ruhige Zeile "läuft ab 12.12. · 14 Aufrufe · 3 Downloads", Schloss bei
  Passwort, "Abgelaufen" grau. Leer: kurzer Satz.
- **Tipp auf eine Zeile:** Aktionsblatt mit Link kopieren, Link teilen (Systemblatt), Bearbeiten
  (bei abgelaufenen "Bearbeiten und wieder einschalten"), Im Ordner zeigen (Pfad über
  `GET /share/info/{id}?owner_extended=true` → `source_uri`), Löschen (Rückfrage,
  `DELETE /share/{id}`).
- **Von der Datei aus:** hat eine eigene Datei `shared == true`, zeigt ihr Aktionsblatt zusätzlich
  "Freigaben verwalten" und Details die Zeile "Freigaben (n)". Beide öffnen ein Blatt mit den
  Freigaben genau dieser Datei (`GET /file/info?extended=true` → `extended_info.shares`), gleiche
  Zeilen und gleiches Aktionsblatt.

### 2. Alle Optionen eines Freigabe-Links

Das heutige `ShareLinkSheet` wird zum Blatt für Anlegen und Bearbeiten (scrollbar), gleiche Felder
wie `ShareSetting.tsx`:

- Passwort (nur beim Anlegen; beim Bearbeiten zeigt eine Zeile "Mit Passwort", weil der Server es
  beim Bearbeiten nicht ändert).
- Ablauf: Chips Nie, 1 Tag, 7 Tage, 30 Tage, "Datum ..." (Datumswahl, Ende des gewählten Tages in
  der Zeitzone der App). Gesendet wird `expire` in Sekunden ab jetzt. Beim Bearbeiten ist die
  bestehende Frist vorgewählt ("bis 12.12.") und wird als Restsekunden neu gesendet, eine abgelaufene
  Frist muss neu gewählt werden.
- Downloads (nur Dateien): Unbegrenzt, 1, 5, 25, "Anzahl ..." (Zahl). Beim Bearbeiten vorgewählt
  ist die Restzahl.
- Nur Ordner: Hochladen Aus / Ansehen und hochladen / Nur hochladen (`allow_upload`,
  `hide_content`), Grenze Unbegrenzt, 1, 5, 20, 100 GB (`upload_limit`, nur bei Hochladen an),
  "Meine Ansicht übernehmen" (`share_view`), "README anzeigen" (`show_readme`).
- Bearbeiten (`POST /share/{id}`) sendet immer alle Felder (der Server ersetzt sie als Ganzes) und
  nie `members`. Ein Mitglieder-Teilen erscheint hier nicht (das ist Punkt 12).
- "Link teilen" gibt es nur mit Gruppen-Bit 2 (Teilen); unbekannt = wie heute.

### 3. Einen Freigabe-Link in der App öffnen

- **Wege hinein:** (a) Link aus einer anderen App "Teilen → Fylvo" (`ACTION_SEND` mit Text: heute
  wird nur ein Datenstrom angenommen); (b) Link ins Suchfeld einfügen: über den Ergebnissen erscheint
  "Freigabe öffnen". Kein Öffnen von https-Links über einen Intent-Filter: ein Link-Filter für
  beliebige Server liesse sich nicht bestätigen und würde fremde `/s/`-Links (andere Dienste)
  einfangen.
- **Prüfung:** Adresse muss `scheme://host[:port]` genau des angemeldeten Servers sein und den Pfad
  `/s/<id>[/<passwort>]` haben (reine Funktion, Unit-Tests). Sonst ein Satz "Dieser Link gehört
  nicht zu deinem Server" und keine Anfrage.
- **Öffnen:** `GET /share/info/{id}?password=`; mit Passwort und noch zu: Passwort-Dialog (Fehler
  40069, zu viele Versuche 40900). Danach wird `cloudreve://<id>[:<pw>]@share` wie ein Ordner von
  "Mit mir geteilt" geöffnet (gleiche Liste, gleiche Rechte aus den Datei-Bits), Titel = Name der
  Freigabe. Eine geteilte Einzeldatei öffnet gleich ihre Vorschau bzw. ihr Aktionsblatt. "Nur
  hochladen" zeigt die leere Liste mit dem "+" und einem Hinweis.
- Das Passwort steckt in der Adresse wie im Web; es wird nicht in Verlauf, Offline, Favoriten,
  Startbildschirm-Verknüpfungen oder Widget übernommen (diese Wege lehnen fremde Freigaben schon
  heute ab; geprüft und mit Tests abgesichert).

### 4. Text- und Markdown-Dateien bearbeiten

- **Wo:** in der Textvorschau oben ein Stift "Bearbeiten", wenn die Datei geändert werden darf
  (`Permissions.canModifyFile`; in verbundenen Speichern nach ihren Bits, gespeichert mit
  `previous_required`), sie als UTF-8 lesbar ist, nicht gekürzt angezeigt wurde und keine
  Arbeitskopie der Offline-Bearbeitung offen ist.
- **Editor:** ganze Seite, Textfeld (Code-Endungen in fester Schrift), oben Zurück, Name,
  "Speichern" (aktiv bei Änderungen). Markdown: Umschalter "Bearbeiten / Vorschau"; die Vorschau
  zeigt Markdown mit Markwon (`io.noties.markwon:core`, Apache 2.0; ohne Bild-Plugin, also lädt sie
  keine Bilder von aussen). Der Text liegt im ViewModel des Editors (überlebt Drehen; nicht in
  `rememberSaveable`, weil bis 2 MB).
- **Speichern:** `PUT /file/content?uri=&previous=<primary_entity beim Laden>` mit den Bytes
  (UTF-8, ein vorhandenes BOM bleibt). Danach gilt die neue `primary_entity` der Antwort.
- **Konflikt 40076:** Dialog "Die Datei wurde inzwischen geändert": "Als Kopie speichern" (neuer Name
  `<Name> (Kopie <Datum>).<ext>`, frei geprüft), "Überschreiben" (ohne `previous`), "Abbrechen".
  40904 (Datei weg): als Kopie speichern anbieten. 40073 gesperrt, 40049 zu gross: Satz.
- **Zurück mit ungespeicherten Änderungen:** Speichern / Verwerfen / Weiter bearbeiten.

### 5. Neue Datei anlegen

- Blatt "+" bekommt "Neue Textdatei" und "Neue Markdown-Notiz" (die Vorlagen des Webs mit Inhalt
  `txt` und `md`; Draw.io und Excalidraw sind nicht gewollt, für Office gibt es keine Vorlage).
  Nur wo hochgeladen werden darf und nicht in verbundenen Speichern.
- Name-Dialog (vorgefüllt "Neue Datei.txt" / "Notiz.md"), `POST /file/create` mit `type: file`,
  `err_on_conflict: true` (Namen-Konflikt 40004 und 40905 als Satz), danach öffnet der Editor.

### 6. Ältere Fassungen

- Aktionsblatt "Versionen" bei eigenen Dateien (nicht in Freigaben, nicht in verbundenen Speichern,
  Datei-Bit 20 wenn vorhanden). Blatt mit `extended_info.entities` vom Typ 0, neueste zuerst:
  Datum, Grösse, "Aktuell".
- Tipp auf eine ältere Fassung: Herunterladen (`POST /file/url` mit `entity`, gleicher Weg wie
  "Herunterladen"), "Wiederherstellen" (`POST /file/version/current`, Rückfrage), "Löschen"
  (`DELETE /file/version`, Rückfrage). Die aktuelle hat nur Herunterladen. Danach lädt das Blatt
  neu und die Liste zeigt Grösse und Datum der neuen aktuellen Fassung.

### 7. Volle Details

- Details lädt beim Öffnen `GET /file/info?extended=true` (bisherige Zeilen sofort, der Rest kommt
  dazu).
- **Kamera:** Gerät, Objektiv, Blende, Belichtung, ISO, Brennweite, Aufnahmezeit (aus `exif:*`).
- **Ort:** `geocoding:*` als Text, sonst Koordinaten. "In Karten-App öffnen" (`geo:`-Intent) nur,
  wenn der Server einen Kartenanbieter hat (`map_provider` nicht leer und nicht `none`). Die App
  lädt selbst keine Kartenbilder.
- **Speicherort-Grösse** bei Ordnern (`storage_used`), **Freigaben (n)** (siehe 1).
- **Verlauf** (eigene Dateien in Meine Dateien und Papierkorb): die neuesten fünf Einträge aus
  `GET /file/activity`, "Alle anzeigen" öffnet eine Seite mit weiteren (`next`). Zeile: was (aus
  `type` und `content`), wer, über (WebDAV, Office, Link), wann.

### 8. Papierkorb leeren

- Im Papierkorb oben "Papierkorb leeren" (im Menü der Kopfzeile), Rückfrage, `DELETE /file/trash`
  (läuft am Server bis zum Ende; eigener, langer Zeitrahmen für diesen Aufruf), danach neu laden.

### 9. Konto und Sicherheit

- **Konto oben:** Profilbild (`GET /user/avatar/{id}`, 404 = Symbol), Name, E-Mail. Tipp öffnet
  **Profil**: Name ändern (`PATCH /user/setting {nick}`), Profilbild ändern (Bild wählen, quadratisch
  zuschneiden, auf 400 px verkleinern, als JPEG `PUT /user/setting/avatar`). Gravatar wird nicht
  angeboten (würde die E-Mail-Kennung an einen fremden Dienst geben).
- **Zeitzone** (nur in der App, der Server hat keine): "Wie Gerät" (Standard) oder eine gewählte
  Zone mit Suche. Alle Datums-Anzeigen der App gehen über eine Stelle, die diese Zone benutzt.
- **Sicherheit** (eigene Seite unter Konto):
  - Passwort ändern (aktuelles, neues, wiederholen; mind. 6 Zeichen; 40069 = aktuelles falsch).
    Nicht bei Konten ohne Passwort (`passwordless`).
  - 2FA: Zustand; Einschalten: `GET /user/setting/2fa` → Geheimnis in Vierergruppen mit Kopieren und
    "In Authenticator-App öffnen" (`otpauth://totp/Fylvo:<E-Mail>?secret=…&issuer=Fylvo`), dann Code
    eingeben → `PATCH {two_fa_enabled: true, two_fa_code}` (40022 falscher Code; nach 10 Minuten neu
    beginnen). Ausschalten mit Code. Immer beide Felder zusammen senden.
  - App-Zugriffe: `oauth_grants` mit zuletzt benutzt, "Widerrufen" mit Rückfrage
    (`DELETE /session/oauth/grant/{client_id}`).
  - Letzte Anmeldungen: `GET /user/setting/activity` (Anmeldung, fehlgeschlagen in Rot, gesperrt,
    Passwort geändert, 2FA an/aus, App widerrufen), mit Gerät (aus dem User-Agent gekürzt), Adresse,
    Zeit, weitere mit `next`.

## Paket 2

### 10. Suche mit Filtern und in Inhalten

- In der Suchleiste ein Filter-Knopf → Blatt: Typ (Alle, Dateien, Ordner), Art (Bilder, Videos,
  Audio, Dokumente), Grösse (ab/bis in Stufen), Geändert (heute, 7 Tage, 30 Tage, 1 Jahr, ab Datum),
  Tags (Chips aus den eigenen Tags). Gewählte Filter stehen als Chips unter der Leiste (antippen =
  weg). Die Such-Adresse baut eine reine Funktion wie `uri.ts` (`type`, `category`, `size_gte`,
  `size_lte`, `updated_gte` in Unix-Sekunden, `meta_tag:<name>=`), Unit-Tests.
- Volltext: nur wenn `full_text_search` an ist, ein Chip "In Inhalten" → `GET /file/search?query=&offset=`;
  Treffer mit einer Zeile Textausschnitt (Hervorhebung aus `<em>` als fett, sonst nur Text). Filter
  gelten dort nicht und werden ausgegraut.

### 11. Archive und Aufgaben

- Aktionsblatt einer Datei mit Endung zip, gz, xz, tar, rar, 7z, bz2 (Gruppen-Bit 5, Datei-Bit 7):
  "Entpacken" → Zielordner (vorhandene Ordnerauswahl, vorgewählt der aktuelle Ordner), bei zip, 7z,
  rar ein freiwilliges Passwort → `POST /workflow/extract`.
- Bei zip und 7z zusätzlich "Inhalt ansehen" (`GET /file/archive`): Liste wie ein Ordner (Ordner im
  Archiv antippbar, Zurück), unten "Alles entpacken" bzw. bei Auswahl "Auswahl entpacken"
  (`file_mask`).
- Auswahl (eine oder mehrere Einträge, Bit 5): "Als ZIP packen" → Name (vorgefüllt) →
  `POST /workflow/archive` mit Ziel im aktuellen Ordner.
- **Aufgaben:** Seite unter Konto ("Aufgaben"), `GET /workflow?category=general`: Art, Quelle/Ziel,
  Zustand, bei laufenden der Fortschritt (`/workflow/progress/{id}` alle 2 s, nur solange die Seite
  offen ist), Fehlertext. Fertiges Entpacken → Tipp öffnet den Zielordner. Nach dem Start einer
  Aufgabe ein Hinweis mit "Ansehen". Abbrechen gibt es am Server nicht.

### 12. Mit Personen teilen

- Aktionsblatt eines eigenen Ordners (Gruppen-Bit 2, nicht in verbundenen Speichern): "Mit Personen
  teilen". Blatt: bestehende Mitglieder (aus der Mitglieder-Freigabe des Ordners in
  `extended_info.shares`, Namen über `GET /user/info/{id}`), je Person "Lesen" / "Bearbeiten",
  Entfernen; "Person hinzufügen": Suche ab 2 Zeichen (`GET /user/search`, ohne mich und vorhandene).
- Speichern: neu `PUT /share {uri, member_only: true, members}`, sonst `POST /share/{id}` mit allen
  Feldern und der ganzen Mitgliederliste. Alle entfernt → "Teilen beenden" löscht die Freigabe.

### 13. Office-Dokumente (nur in einer anderen App öffnen)

- Kein eingebauter Editor. Geprüft und verbessert wird der heutige Weg "Öffnen mit": richtiger
  MIME-Typ (aus der Endung, auch wenn das System keinen kennt), Leserecht für die andere App, ein
  Satz statt Stille, wenn keine App passt, Fortschritt bei grossen Dateien, auch aus "Mit mir
  geteilt" und verbundenen Speichern (Export-Endung). Im Emulator mit einer Office-App geprüft,
  soweit eine installierbar ist.
- Änderungen kommen heute nur bei offline gehaltenen Dateien zurück ("Bearbeiten"). Offene Frage an
  Sven (nicht ungefragt bauen): "Bearbeiten" auch ohne offline halten (die App holt eine
  Arbeitskopie und lädt jedes Speichern als neue Fassung hoch, mit dem vorhandenen Weg).

### 14. Verbundene Speicher in der App verbinden, neu verbinden, trennen

- **Trennen** (`DELETE /fylvo/connected/{id}`, Rückfrage) und **Umbenennen** (`PATCH`) gehen ohne
  Änderung am Server: Seite "Verbundene Speicher" unter Konto.
- **Verbinden / Neu verbinden** braucht den Server: der Anmeldeweg bindet die Anmeldung an ein
  Cookie im Browser und endet auf der Webseite; Google lässt die Anmeldung in einer WebView nicht zu.
  Ohne Server-Änderung bleibt nur, die Webseite im Browser zu öffnen (wie heute). Frage an den
  Haupt-Thread (siehe unten).

### 15. Ganzen Ordner hochladen

- Blatt "+": "Ordner hochladen" → Ordnerwahl des Systems (`OpenDocumentTree`, Leserecht für die
  Dauer der Uploads dauerhaft genommen und danach wieder freigegeben). Die App liest den Baum, zeigt
  "n Dateien, x MB" zur Bestätigung (ab 2000 Dateien deutlich), legt die Ordner an (auch leere) und
  reiht die Dateien je Unterordner in die vorhandene Upload-Warteschlange (gleiche Konfliktregel wie
  heute: vorhandener Ordner gleichen Namens wird ergänzt, gleichnamige Dateien bekommen einen neuen
  Namen). Nicht in verbundenen Speichern.

## Etappen (je ein Commit, Unit-Tests, im Emulator durchgespielt, danach Push)

0. Grundlage: Konto-Daten mit Gruppen-Bits, Explorer-Einstellungen, Fehlertexte.
1. Freigaben verwalten und alle Optionen (1, 2).
2. Freigabe-Link öffnen (3).
3. Text bearbeiten und neue Datei (4, 5).
4. Versionen, Details, Papierkorb leeren (6, 7, 8).
5. Konto und Sicherheit (9).
   → Code-Prüfung Block 1 (rat, höchstens 5 Runden).
6. Suche mit Filtern und Volltext (10).
7. Archive und Aufgaben (11).
8. Mit Personen teilen (12).
9. Office öffnen (13) und Ordner hochladen (15).
10. Verbundene Speicher verwalten (14, Umfang nach Antwort).
11. Version 1.6.0, README.
   → Code-Prüfung Block 2 (rat, höchstens 5 Runden).

Testserver lokal aus `main` (eigener Port, eigener Datenordner), Emulator `fylvo_test`.
Nicht prüfbar ohne echte Dienste: Office-Bearbeitung am Server, echte Google/Microsoft-Konten,
Volltextsuche (braucht einen Suchdienst am Server; geprüft wird, dass sie ohne ihn verborgen ist).
