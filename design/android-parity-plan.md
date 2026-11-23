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
  `rememberSaveable`, nie DataStore, nie im Log); das 2FA-Geheimnis steht auf einer eigenen Seite,
  solange sie offen ist mit `FLAG_SECURE` am Fenster. Ein Freigabe-Link wird nur angenommen, wenn
  er zum angemeldeten Server gehört (siehe 3); sonst geht keine Anfrage hinaus.
- **Freigabe-Passwörter nie auf die Platte:** eine Adresse `cloudreve://<id>:<pw>@share...` lebt nur
  im Speicher (ViewModel). Eine Prüfung `CrUri.hasSecret(uri)` sperrt jeden Weg, der etwas sichert
  oder weitergibt: offline halten, Favoriten, Orte/Reiter, Startbildschirm-Verknüpfung, Widget,
  Upload-Warteschlange, `rememberSaveable` (z. B. `uploadTarget`), letzte Dateien. Dazu
  `CrUri.shareId(uri)`, das `:<pw>` abschneidet (heute schickt `MobilePrefs.starState` die id samt
  Passwort an `/share/info/{id}`). Je Weg ein Unit-Test. Hochladen in einen Link mit Passwort gibt
  es in 1.6.0 nicht (die Warteschlange müsste das Passwort sichern): dort kein "+", sondern ein
  kurzer Hinweis; in Links ohne Passwort schon. Archiv-Aufgaben mit einer solchen Adresse auch nicht
  (der Server speichert Quelle und Ziel der Aufgabe in seiner Datenbank).
- **Keine Adressen im Log:** `userError` schreibt heute die Fehlermeldung ins Log, und manche
  Meldungen enthalten eine Adresse (`FileRepository.kt:144`). Log nur noch Fehlerart und Code;
  Aufrufe mit Freigabe-Passwort und alle Konto-Aufrufe mit Passwort oder 2FA-Code laufen über den
  vorhandenen Client ohne Weiterleitungen (`authService()`).
- Kopierte Freigabe-Links werden in der Zwischenablage als vertraulich markiert
  (`ClipDescription.EXTRA_IS_SENSITIVE`). Das 2FA-Geheimnis wird nicht kopiert.
- **Sitzung:** jede ändernde Aktion und jeder Entwurf gehört zur Sitzung, in der er begann
  (`SessionTag`); nach Abmelden oder Kontowechsel wird nichts davon gegen das neue Konto ausgeführt,
  offene Freigabe-Seiten werden geschlossen.
- **Texte** Deutsch (Schweizer Schreibweise, kein "ß") und Englisch.

## Grundlage (Etappe 0)

- `GET /api/v4/user/me` einmal pro Sitzung (und beim Öffnen von Konto): `id`, `nickname`, `avatar`,
  `group.permission` (Base64-Bitsatz). Gehalten im Speicher bei `SessionManager`/einem kleinen
  `AccountRepository`, mit der Sitzungsmarke (`SessionTag`) wie die Anpassungen. Fehlt die Antwort,
  gelten die Gruppen-Rechte als unbekannt: dann erscheinen Teilen, Mit Personen teilen und Archive
  nicht (erst nach der Antwort).
- Gruppen-Bits (wie `frontend/src/api/user.ts`): 2 Teilen, 5 Archiv-Aufgaben, 7 fremde Freigaben
  öffnen. Datei-Bits (wie heute `Permissions`): 0 anlegen, 1 umbenennen, 6 hochladen (auch: Inhalt
  ersetzen), 7 herunterladen, 18 teilen, 20 Versionen. Rechte werden **je Aktion** geprüft, je eine
  kleine Funktion in `Permissions` mit Tests:
  - teilen: Besitz + Gruppen-Bit 2 + Datei-Bit 18;
  - Inhalt speichern (wie `useActionDisplayOpt.ts`): in eigenen Dateien Besitz; in Freigaben und
    verbundenen Speichern `canModifyFile` (Bit 1) **und** Bit 6. Besucher eines Upload-Links haben
    nur Bit 6 und dürfen nicht ersetzen;
  - neue Textdatei: wie Inhalt speichern, aber am Ordner (also nicht in Upload-Links);
  - Archiv packen/entpacken: Gruppen-Bit 5, lesbare Quellen (Bit 7), Ziel mit Bit 0 und Bit 6;
  - Versionen: Besitz + Bit 20.
  Neue rechtepflichtige Aktionen erscheinen erst, wenn die Gruppen-Bits da sind.
- `GET /site/config/explorer` liest zusätzlich `map_provider` und `full_text_search`.

## Paket 1

### 1. Eigene Freigaben verwalten

- **Wo:** im Reiter "Geteilt" stehen oben zwei Chips "Mit mir geteilt" und "Von mir geteilt" (wie die
  Orte-Chips über "Meine Dateien"). "Von mir geteilt" öffnet die Seite **Freigaben** (Seite wie
  Favoriten, `PAGE_SHARES`). Dieselbe Seite auch unter Konto ("Meine Freigaben"), weil der Reiter
  "Geteilt" abgewählt sein kann.
- **Seite:** `GET /share?page_size=50&order_direction=desc`, weiter mit `next_page_token` =
  `pagination.next_token` der Antwort (Test mit zwei Seiten). Zeile: Symbol nach Typ, Name, eine
  ruhige Zeile "läuft ab 12.12. · 14 Aufrufe · 3 Downloads", Schloss bei Passwort, "Mit n Personen"
  bei `member_only`, "Abgelaufen" grau. Leer: kurzer Satz.
- **Tipp auf eine Zeile:** Aktionsblatt mit Link kopieren, Link teilen (Systemblatt), Bearbeiten
  (nur mit bekanntem Gruppen-Bit 2, der Server prüft es auch beim Ändern; gilt für beide Eingänge),
  Im Ordner zeigen, Löschen (Rückfrage, `DELETE /share/{id}`). Den Pfad holt die App wie das Web
  (`share.ts:225`): `GET /share/info/{id}?owner_extended=true&password=<pw aus der Zeile>` (der
  Besitzer bekommt das Passwort in der Liste; nur im Speicher) → `source_uri`. Bei einer
  Einzeldatei ist `source_uri` der Ordner: "Im Ordner zeigen" öffnet ihn, "Bearbeiten" nimmt
  `source_uri` + `name`. Test mit einer Einzeldatei-Freigabe mit Passwort. Bei `member_only` kein Link, "Bearbeiten" öffnet das
  Personen-Blatt (12). **Abgelaufene** Freigaben: nur Löschen, dazu der Hinweis "Wieder einschalten
  über die Datei" (der Server gibt für sie kein `source_uri` heraus, und Bearbeiten braucht die
  Adresse der Datei).
- **Von der Datei aus:** hat eine eigene Datei `shared == true`, zeigt ihr Aktionsblatt zusätzlich
  "Freigaben verwalten" und Details die Zeile "Freigaben (n)". Beide öffnen ein Blatt mit den
  Freigaben genau dieser Datei (`GET /file/info?extended=true` → `extended_info.shares`), gleiche
  Zeilen und gleiches Aktionsblatt; hier auch "Bearbeiten und wieder einschalten" für abgelaufene.

### 2. Alle Optionen eines Freigabe-Links

Das heutige `ShareLinkSheet` wird zum Blatt für Anlegen und Bearbeiten (scrollbar), gleiche Felder
wie `ShareSetting.tsx`:

- Passwort (nur beim Anlegen; nur Buchstaben und Ziffern, höchstens 32, in der App geprüft; beim
  Bearbeiten zeigt eine Zeile "Mit Passwort", weil der Server es beim Bearbeiten nicht ändert).
- Ablauf: Chips Nie, 1 Tag, 7 Tage, 30 Tage, "Datum ..." (Datumswahl bis höchstens 10 Jahre, Grenze des Servers; Ende des gewählten Tages in
  der Zeitzone des Geräts). Gesendet wird `expire` in Sekunden ab jetzt. Beim Bearbeiten ist die
  bestehende Frist als eigener Chip vorgewählt ("bis 12.12.") und wird als Restsekunden neu
  gesendet (aufgerundet, nie 0, weil 0 die Frist löscht); eine abgelaufene Frist muss neu gewählt werden.
- Downloads (nur Dateien): Unbegrenzt, 1, 5, 25, "Anzahl ..." (Zahl). Beim Bearbeiten ist die
  Restzahl als eigener Chip vorgewählt; ist sie 0, muss neu gewählt werden (0 hiesse unbegrenzt).
  Bleibt sie unverändert, holt die App sie direkt vor dem Speichern neu (sonst kämen inzwischen
  verbrauchte Downloads zurück).
- Nur Ordner: Hochladen Aus / Ansehen und hochladen / Nur hochladen (`allow_upload`,
  `hide_content`), Grenze Unbegrenzt, 1, 5, 20, 100 GB (`upload_limit`, nur bei Hochladen an; ein im
  Web gesetzter anderer Wert erscheint als eigener Chip und bleibt), "Meine Ansicht übernehmen"
  (`share_view`), "README anzeigen" (`show_readme`).
- Bearbeiten (`POST /share/{id}`) sendet immer alle Felder (der Server ersetzt sie als Ganzes) und
  `members` nie (weder leer noch null im JSON). `ShareCreateRequest` wird dafür bereinigt (tote
  Felder `allow_edit`, `preview_only`, `upload_only` weg; `hide_content`, `upload_limit` dazu).
- "Link teilen" nach der Rechte-Funktion oben (Gruppen-Bit 2, Datei-Bit 18, Besitz).

### 3. Einen Freigabe-Link in der App öffnen

- **Wege hinein:** (a) Link aus einer anderen App "Teilen → Fylvo" (`ACTION_SEND` mit Text: heute
  wird nur ein Datenstrom angenommen); (b) Link ins Suchfeld einfügen: über den Ergebnissen erscheint
  "Freigabe öffnen". Kein Öffnen von https-Links über einen Intent-Filter: ein Link-Filter für
  beliebige Server liesse sich nicht bestätigen und würde fremde `/s/`-Links (andere Dienste)
  einfangen.
- **Prüfung:** Pfad `/s/<id>[/<passwort>]`, und `scheme://host:port` (Standard-Ports 80/443
  ergänzt) gleich der Adresse, mit der die App angemeldet ist (die Site-Adresse des Servers ist
  nicht öffentlich abfragbar; ein Link über eine andere Adresse desselben Servers wird darum
  abgelehnt, mit dem Satz). Reine Funktion mit Tests. Sonst ein Satz "Dieser Link gehört nicht zu
  deinem Server" und keine Anfrage. Ein eingefügter Link wird vor dem Start der Namens- und der
  Volltextsuche erkannt (er löst keine Suche aus). Ein behandelter Text-Intent wird danach
  geleert, damit Drehen ihn nicht nochmals öffnet.
- **Öffnen:** `GET /share/info/{id}?password=`: `unlocked == false` mit `password_protected` →
  Passwort-Dialog (falsches Passwort = wieder `unlocked == false`; 40900 = zu viele Versuche; 404 =
  abgelaufen oder weg). Gehört die Freigabe jemand anderem und fehlt Gruppen-Bit 7: ein Satz ("Dein
  Konto darf keine Freigaben öffnen"); der Besitzer braucht das Bit nicht und bekommt das Passwort
  in der Antwort (die Liste prüft es auch bei ihm). Danach wird `cloudreve://<id>[:<pw>]@share` wie ein Ordner von "Mit mir geteilt" geöffnet
  (gleiche Liste, Rechte aus den Datei-Bits; 40069 beim Auflisten = Passwort ungültig geworden),
  Titel = Name der Freigabe. Eine geteilte Einzeldatei öffnet gleich ihre Vorschau bzw. ihr
  Aktionsblatt. "Nur hochladen" zeigt die leere Liste mit dem "+" und einem Hinweis.
- Mitglieder-Freigaben ("Mit mir geteilt") haben kein Passwort und bleiben, wie sie sind (offline
  halten geht dort weiter). Für Links mit Passwort gilt die Regel "nie auf die Platte" oben.

### 4. Text- und Markdown-Dateien bearbeiten

- **Wo:** in der Textvorschau oben ein Stift "Bearbeiten", wenn Inhalt speichern erlaubt ist (Rechte
  oben, also auch in verbundenen Speichern Bit 1 und Bit 6, dazu herunterladbar), die Datei höchstens 256 KB hat
  und als UTF-8 lesbar ist, und keine Arbeitskopie der Offline-Bearbeitung offen ist (vor dem
  Speichern nochmals geprüft).
- **Laden:** der Editor lädt selbst genau die Fassung `primary_entity` (`POST /file/url` mit
  `entity`) und merkt sie sich samt Datei-ID; kein Rückgriff auf eine ältere Offline-Kopie (die
  Vorschau darf das weiter, bleibt dann aber nur lesbar). Eine leere Datei ohne Fassung (z. B. im Web
  angelegt) startet mit leerem Text; ihre `primary_entity` ist dann die kodierte Null-Fassung des
  Servers und wird genauso als `previous` mit `previous_required=true` gesendet (im Emulator geprüft,
  sonst vorher `GET /file/info` und bei neuer Fassung der Konflikt-Dialog). Bekannte, bewusst
  hingenommene Lücke: alle Dateien ohne Fassung haben dieselbe Null-Fassung; wird die leere Datei
  zwischen Laden und Speichern durch eine andere leere ersetzt, trifft das Speichern diese. Verloren
  geht dabei nichts, denn beide sind leer. Direkt vor dem Speichern prüft die App zusätzlich, dass
  die Datei an der Adresse noch dieselbe ID hat; sonst Konflikt-Dialog.
- **Editor:** ganze Seite, Textfeld mit `TextFieldState` (Code-Endungen in fester Schrift), oben
  Zurück, Name, "Speichern" (aktiv bei Änderungen). Markdown: Umschalter "Bearbeiten / Vorschau";
  die Vorschau mit einer gepflegten Compose-Bibliothek (`com.mikepenz:multiplatform-markdown-renderer`,
  Apache 2.0, ohne Bild-Modul: lädt keine Bilder von aussen). Der Text liegt im ViewModel des
  Editors (überlebt Drehen, nicht in `rememberSaveable`). Links in der Vorschau öffnen nur, wenn sie
  mit `http://` oder `https://` beginnen.
- **Speichern:** `PUT /file/content?uri=&previous=<geladene Fassung>&previous_required=true` mit den
  Bytes (UTF-8, ein vorhandenes BOM bleibt). Danach gilt die `primary_entity` der Antwort.
- **Konflikt 40076:** Dialog "Die Datei wurde inzwischen geändert": "Als Kopie speichern", "Trotzdem
  überschreiben" (neu mit der dann aktuellen Fassung als `previous`, nach erneuter Prüfung, dass es
  dieselbe Datei-ID ist), "Abbrechen". 40904 (Datei weg): nur "Als Kopie speichern". "Als Kopie"
  lädt den Text als neue Datei hoch (vorhandener Upload-Weg, der nur anlegt: belegter Name = 40004 oder
  40905, dann der nächste Name; wie das Web in verbundenen Speichern, `viewer.ts:511`), Name
  `<Name> (copy <Datum>).<ext>` (wie die Konfliktkopien der Offline-Bearbeitung). 40073 gesperrt, 40049 zu gross: Satz. 40927 (verbundener
  Speicher, Ausgang unbekannt): Entwurf bleibt, Hinweis, neu laden vor erneutem Versuch. Verbundene
  Speicher nehmen keine leere Datei an: Speichern bei leerem Text dort gesperrt, mit Satz.
- **Zurück mit ungespeicherten Änderungen:** Speichern / Verwerfen / Weiter bearbeiten.

### 5. Neue Datei anlegen

- Blatt "+" bekommt "Neue Textdatei" und "Neue Markdown-Notiz" (die Vorlagen des Webs mit Inhalt
  `txt` und `md`; Draw.io und Excalidraw sind nicht gewollt, für Office gibt es keine Vorlage).
  Nach der Rechte-Regel "neue Textdatei", nicht in verbundenen Speichern.
- Name-Dialog (vorgefüllt "Neue Datei.txt" / "Notiz.md"), danach öffnet der Editor mit leerem
  Text; am Server entsteht die Datei erst beim ersten Speichern, als Upload, der nur anlegt
  (belegter Name 40004 und 40905 als Satz, Name ändern). So gibt es keine leere Datei ohne Fassung.

### 6. Ältere Fassungen

- Aktionsblatt "Versionen" bei eigenen Dateien (nicht in Freigaben, nicht in verbundenen Speichern,
  Datei-Bit 20 wenn vorhanden). Nicht, solange eine Arbeitskopie der Offline-Bearbeitung offen ist
  (deren Grundlage würde sonst unbemerkt veraltet). Blatt mit `extended_info.entities` vom Typ 0, neueste zuerst:
  Datum, Grösse, "Aktuell".
- Tipp auf eine ältere Fassung: Herunterladen (`POST /file/url` mit `entity`, gleicher Weg wie
  "Herunterladen"), "Wiederherstellen" (`POST /file/version/current`, Rückfrage), "Löschen"
  (`DELETE /file/version`, Rückfrage). Die aktuelle hat nur Herunterladen. Danach lädt das Blatt
  neu und die Liste zeigt Grösse und Datum der neuen aktuellen Fassung.

### 7. Volle Details (Ort, Kamera, Verlauf)

- Details lädt beim Öffnen `GET /file/info?extended=true` (bisherige Zeilen sofort, der Rest kommt
  dazu).
- **Kamera:** Gerät, Objektiv, Blende, Belichtung, ISO, Brennweite, Aufnahmezeit (aus `exif:*`).
- **Ort:** `geocoding:*` als Text, sonst Koordinaten. "In Karten-App öffnen" (`geo:`-Intent) nur,
  wenn der Server einen Kartenanbieter hat (`map_provider` nicht leer und nicht `none`). Die App
  lädt selbst keine Kartenbilder.
- **Speicherort-Grösse** bei Ordnern (`storage_used`), **Freigaben (n)** (siehe 1).
- **Verlauf** (eigene Dateien in Meine Dateien und Papierkorb): die neuesten zehn Einträge aus
  `GET /file/activity` direkt in den Details, keine eigene Seite. Zeile: was (aus `type` und
  `content`), wer, über (WebDAV, Office, Link), wann.

### 8. Papierkorb leeren

- Im Papierkorb oben "Papierkorb leeren" (im Menü der Kopfzeile), Rückfrage, `DELETE /file/trash`
  (läuft am Server bis zum Ende; eigener, langer Zeitrahmen für diesen Aufruf), danach neu laden.

### 9. Konto und Sicherheit

- **Konto oben:** Profilbild, Name, E-Mail. Das Bild wird nur geladen, wenn `/user/me` `avatar ==
  "file"` meldet (`GET /user/avatar/{id}`, ohne Weiterleitungen; nach einem neuen Bild mit
  `nocache=true`); sonst Initialen. So geht nie eine Anfrage an Gravatar. Tipp öffnet **Profil**:
  Name ändern (`PATCH /user/setting {nick}`), Profilbild ändern (Bild wählen, quadratisch
  zuschneiden, auf 400 px verkleinern, als JPEG `PUT /user/setting/avatar`). Gravatar wird nicht
  angeboten.
- **Zeitzone: nicht gebaut.** Der Server kennt keine; das Web merkt sie sich nur im Browser. Die App
  zeigt alle Zeiten in der Zeitzone des Geräts, das ist dasselbe Ergebnis ohne eigene Einstellung.
- **Sicherheit** (eigene Seite unter Konto):
  - Passwort ändern (aktuelles, neues, wiederholen; mind. 6 Zeichen; 40069 = aktuelles falsch).
    Nicht bei Konten ohne Passwort (`passwordless`). Endet die Sitzung danach, läuft der vorhandene
    Weg für abgelaufene Sitzungen (Arbeitskopien bleiben).
  - 2FA: Zustand; Einschalten: `GET /user/setting/2fa` → eigene Seite (Fenster mit `FLAG_SECURE`)
    mit dem Geheimnis in Vierergruppen (nicht kopierbar) und "In Authenticator-App öffnen"
    (`otpauth://totp/Fylvo:<E-Mail>?secret=…&issuer=Fylvo`), dann Code eingeben →
    `PATCH {two_fa_enabled: true, two_fa_code}` (40022 falscher Code; nach 10 Minuten neu beginnen).
    Ausschalten mit Code. Immer beide Felder zusammen senden.
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
  weg). Die Such-Adresse baut eine reine Funktion wie `uri.ts` (`name`, `case_folding`, `type`,
  `size_gte`, `size_lte`, `updated_gte` in Unix-Sekunden, `meta_tag:<name>=`), Unit-Tests, die
  prüfen, dass alle Filter zusammen beim Server ankommen. **Art** (`category`) ersetzt am Server die
  ganze Suche; deshalb schliesst die Art die anderen Filter aus (wählt man sie, werden Name und die
  übrigen Filter ausgegraut).
- Volltext: nur wenn `full_text_search` an ist, ein Chip "In Inhalten" → `GET /file/search?query=&offset=`;
  Treffer mit einer Zeile Textausschnitt (Hervorhebung aus `<em>` als fett, sonst nur Text). Filter
  gelten dort nicht und werden ausgegraut.

### 11. Archive und Aufgaben

- Aktionsblatt eines Archivs (zip, rar, 7z, tar, tar.gz/tgz, tar.xz, tar.bz2; einzelne .gz/.xz/.bz2
  ohne tar kann der Server nicht entpacken) (Gruppen-Bit 5, Datei-Bit 7):
  "Entpacken" → Zielordner (vorhandene Ordnerauswahl, vorgewählt der aktuelle Ordner, wenn er als Ziel erlaubt ist (Bit 0 und Bit 6, dieselbe Regel wie oben), sonst Meine Dateien) →
  `POST /workflow/extract` (immer alles). **Kein Archiv-Passwort** in 1.6.0: der Server speichert
  es im Klartext im Zustand der Aufgabe (an den Haupt-Thread gemeldet).
- Bei zip und 7z zusätzlich "Inhalt ansehen" (`GET /file/archive`, Sven wollte "hineinsehen"):
  Liste wie ein Ordner (Ordner im Archiv antippbar, Zurück), unten "Entpacken". Keine Auswahl
  einzelner Einträge.
- Auswahl (eine oder mehrere Einträge, Bit 5): "Als ZIP packen" → Name (vorgefüllt) →
  `POST /workflow/archive` mit Ziel im aktuellen Ordner.
- **Aufgaben:** Seite unter Konto ("Aufgaben"), `GET /workflow?category=general&page_size=20`
  (weiter mit `next_page_token`): Art, Quelle/Ziel, Zustand, Fehlertext. Solange die Seite offen ist
  und eine Aufgabe läuft, wird nur die erste Seite der Liste alle 3 s neu geholt (zeigt Phase und
  Ende); kein eigener Fortschritts-Aufruf je Aufgabe. Fertiges Entpacken → Tipp öffnet den Zielordner. Nach dem Start einer
  Aufgabe ein Hinweis mit "Ansehen". Abbrechen gibt es am Server nicht.

### 12. Mit Personen teilen

- Aktionsblatt eines eigenen Ordners (gleiche Rechte wie Teilen: Besitz, Gruppen-Bit 2, Datei-Bit 18; nicht in verbundenen Speichern): "Mit Personen
  teilen". Blatt: bestehende Mitglieder (aus der Mitglieder-Freigabe des Ordners in
  `extended_info.shares`, Namen über `GET /user/info/{id}`), je Person "Lesen" / "Bearbeiten",
  Entfernen; "Person hinzufügen": Suche ab 2 Zeichen (`GET /user/search`, ohne mich und vorhandene).
- Speichern: neu `PUT /share {uri, member_only: true, members}`, sonst `POST /share/{id}` mit allen
  Feldern (der Server ersetzt sie): die laufende Frist als Restsekunden (aufgerundet; eine abgelaufene muss neu gewählt werden), `share_view` und
  `show_readme` aus der Besitzer-Antwort, dazu die ganze Mitgliederliste. Alle entfernt → "Teilen
  beenden" löscht die Freigabe.

### 13. Office-Dokumente (nur in einer anderen App öffnen)

- Kein eingebauter Editor, keine Abfrage der Office-Betrachter des Servers. Geprüft und verbessert
  wird der heutige Weg "Öffnen mit": richtiger MIME-Typ (aus der Endung bzw. der Export-Endung
  verbundener Speicher, auch wenn das System keinen kennt), Leserecht für die andere App, ein
  Satz statt Stille, wenn keine App passt, Fortschritt bei grossen Dateien, auch aus "Mit mir
  geteilt" und verbundenen Speichern (Export-Endung). Im Emulator mit einer Office-App geprüft,
  soweit eine installierbar ist.
- Entscheid von Sven (2026-11-23): "Bearbeiten" auch ohne offline halten. Die App holt genau die
  aktuelle Fassung als eigenständige Arbeitskopie; der vorhandene Weg lädt jedes ruhige Speichern als
  neue Fassung hoch (bei Konflikt eine Kopie). Die Datei wird beim Server über ihre ID verfolgt. Eine
  Arbeitskopie, die ganz am Server ist und einen Tag ruht, wird von selbst abgeschlossen und einen Tag
  später gelöscht; ein spätes Speichern geht dann als Konflikt-Kopie hoch.

### 14. Verbundene Speicher in der App verbinden, neu verbinden, trennen

- **Trennen** (`DELETE /fylvo/connected/{id}`, Rückfrage) und **Umbenennen** (`PATCH`) gehen ohne
  Änderung am Server: Seite "Verbundene Speicher" unter Konto. Nach dem Trennen werden Reiter, Orte
  und wartende Uploads, die auf diesen Speicher zeigen, aufgeräumt.
- **Verbinden / Neu verbinden** über den Weg des Servers für Apps (`app_signin`): Prüfwert des
  App-Geheimnisses an `authorize`, Start-Adresse sofort im Custom Tab, Rück-Link
  `fylvo://connected?attempt=…&finish=…`, Vorschau des Anbieter-Kontos und Speichern erst nach "Ja"
  (Neu verbinden ohne Rückfrage). Ein Versuch zur Zeit, App-privat gespeichert, höchstens 20 Minuten.
  Ohne `app_signin` öffnet wie bisher die Webseite.

### 15. Ganzen Ordner hochladen

- Blatt "+": "Ordner hochladen" → Ordnerwahl des Systems (`OpenDocumentTree`). Konto und ID des
  Zielordners werden vor der Wahl festgehalten und vor dem Anlegen geprüft. Das Leserecht am Baum
  wird dauerhaft genommen, jede zugehörige Upload-Gruppe trägt die Baum-Adresse, und das Recht wird
  freigegeben, wenn die letzte Gruppe fertig oder abgebrochen ist, beim Abmelden, und beim
  nächsten Start für Bäume ohne offene Gruppe (falls die App dazwischen beendet wurde). Die App liest den Baum, zeigt
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
