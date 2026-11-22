# Fylvo: Anpassung (Seitenleiste, Favoriten, Tags, Startseite, Ansicht, Aussehen)

Stand 2026-11-22. Auftrag von Sven: mehr Anpassung auf Web und Handy. Ideenkatalog mit Sven
abgestimmt, alle vier Bündel gewählt.

## Entscheide von Sven (2026-11-22)

1. Gebaut wird: Seitenleiste frei zusammenstellen, Favoriten (Stern), Farben und Tags sichtbar
   machen, Startseite, Ansicht (Dichte, Standard-Ansicht und Sortierung), Aussehen (hell, dunkel,
   wie Gerät, Akzentfarbe).
2. Anpassungen hängen am **Konto** und sind auf Web, Handy und später Desktop gleich. Nur was vom
   Bildschirm abhängt (Breite der Seitenleiste, Ordnerbaum auf- und zugeklappt) bleibt pro Gerät.
3. Tags und Ordnerfarben bleiben wie heute an der Datei und für Freigabe-Empfänger sichtbar.
   Favoriten sind privat.
4. Handy (eigener Thread, baut auf dieser Schnittstelle): Reiter unten wählbar und sortierbar,
   Wischgesten belegbar, Schnellzugriffe am App-Symbol und Widget, Teilen in Fylvo mit Zielordner.

## Grundsätze

- Ohne Anpassung sieht alles aus wie heute. Neue Felder sind leer, der Server liefert Standardwerte.
- Der Server kennt die Standardwerte, die Apps nicht. `GET` liefert immer die fertige, wirksame
  Einstellung (gespeichert über Standard gelegt). So gibt es die Logik nur einmal.
- Keine neue Tabelle, keine Änderung am Datenbank-Schema: alles liegt im bestehenden JSON-Feld
  `settings` des Benutzers (`inventory/types.UserSetting`).
- Rechte: nichts davon gibt Zugriff. Favoriten und Verknüpfungen werden beim Anzeigen durch die
  normalen Zugriffsprüfungen geladen; was jemand nicht mehr öffnen darf, erscheint nicht.

## Schnittstelle am Server (neu, alle unter `/api/v4/fylvo`, nur angemeldet)

### Einstellungen

`GET /api/v4/fylvo/prefs` liefert:

```json
{
  "sidebar": {
    "items": [
      { "type": "home" },
      { "type": "my_files" },
      { "type": "favorites" },
      { "type": "tags" },
      { "type": "shared_with_me" },
      { "type": "category", "category": "image", "hidden": true },
      { "type": "category", "category": "video", "hidden": true },
      { "type": "category", "category": "audio", "hidden": true },
      { "type": "category", "category": "document", "hidden": true },
      { "type": "trash" },
      { "type": "link", "uri": "cloudreve://my/Steuern", "name": "Steuern" }
    ]
  },
  "home": {
    "widgets": [
      { "type": "recent" },
      { "type": "favorites" },
      { "type": "links" },
      { "type": "tags", "hidden": true },
      { "type": "storage" }
    ]
  },
  "start_page": "my_files",
  "view": { "density": "compact", "layout": "list", "sort_by": "name", "sort_direction": "asc" },
  "appearance": { "mode": "system", "accent": "indigo" },
  "mobile": {},
  "favorites": ["hashid1", "hashid2"]
}
```

- Werte:
  - `sidebar.items[].type`: `home`, `my_files`, `favorites`, `tags`, `shared_with_me`, `trash`,
    `category` (mit `category` = `image|video|audio|document`), `link` (mit `uri`, optional `name`),
    `heading` (mit `name`, eigene Abschnitts-Überschrift). `hidden` ist bei allen ausser `link` und
    `heading` erlaubt (diese werden gelöscht statt versteckt). Jeder feste Typ höchstens einmal,
    `category` einmal pro Kategorie. Höchstens 60 Einträge, Name höchstens 64 Zeichen.
  - `home.widgets[].type`: `recent`, `favorites`, `links` (die Verknüpfungen der Seitenleiste),
    `tags`, `storage`; jeder höchstens einmal, `hidden` erlaubt.
  - `start_page`: `home` oder `my_files`.
  - `view.density`: `compact` (heute), `normal`, `comfortable`. `view.layout`: `list|grid|gallery`.
    `view.sort_by`: `name|size|updated_at|created_at`. `view.sort_direction`: `asc|desc`.
    Das ist die **Standard**-Ansicht; die Ansicht pro Ordner (bestehend, `PATCH /file/view`) geht vor.
  - `appearance.mode`: `system|light|dark`. `appearance.accent`: eine von sechs festen Farben
    (Tabelle unten), Standard `indigo` = `#7484F4` wie heute.
  - `mobile`: freies JSON-Objekt für die Handy-App (Reiter, Wischgesten), höchstens 8 KB. Der Server
    prüft nur die Grösse, das Format legt der Handy-Thread fest.
  - `favorites`: nur lesen, die Datei-IDs (Hash-IDs wie `id` in Datei-Antworten) aller Favoriten,
    neuester zuerst. Damit zeigen die Apps den Stern ohne die Liste aufzulösen.
- Standard, wenn nichts gespeichert ist: wie oben, aber Seitenleiste wie heute (Kategorien sichtbar,
  `home`, `favorites`, `tags` dabei, `heading` keine), plus alle bestehenden angehefteten Einträge
  (`settings.pined`) als `link` am Schluss. Startseite `my_files`, damit nach dem Update alles gleich
  startet.
- Fehlende Felder in gespeicherten Daten werden mit dem Standard aufgefüllt. Neue feste Einträge, die
  eine spätere Version einführt und die in einer gespeicherten Seitenleiste fehlen, hängt der Server
  am Schluss an (versteckt), damit sie im Editor auftauchen.

`PATCH /api/v4/fylvo/prefs` mit einem Teil der obersten Felder, z. B. `{"sidebar": {...}}` oder
`{"appearance": {"mode": "dark"}}`. Jeder mitgeschickte oberste Teil ersetzt den gespeicherten Teil
ganz (bei `view` und `appearance` werden fehlende Unterfelder mit dem Standard gefüllt). `favorites`
ist hier nicht schreibbar. Ungültige Werte: Fehler `40001` (Parameter), nichts wird gespeichert.
Antwort: die neue wirksame Einstellung (wie `GET`). Zwei Geräte, die verschiedene Teile ändern,
überschreiben sich nicht.

`DELETE /api/v4/fylvo/prefs` setzt alles ausser Favoriten auf den Standard zurück ("Zurücksetzen").

### Favoriten

- `GET /api/v4/fylvo/favorites` liefert `{"files": [Datei-Antwort wie bei GET /file], "unavailable": 0}`.
  Die `path` jeder Datei ist eine Adresse, die der Benutzer öffnen kann: eigene Dateien unter
  `cloudreve://my/...`, Dateien in gemeinsamen Ordnern über die Mitglieder-Freigabe
  (`cloudreve://<share>@share/...`). Reihenfolge: zuletzt markiert zuerst. Favoriten im Papierkorb
  oder ohne Zugriff fehlen in `files` und zählen in `unavailable`.
- `PUT /api/v4/fylvo/favorites` mit `{"uris": ["cloudreve://my/a.pdf", ...]}`: markiert. Jede Adresse
  muss sich öffnen lassen (eigene Datei oder gemeinsamer Ordner, in dem man Mitglied ist). Adressen
  öffentlicher Freigabe-Links werden abgelehnt (`40001`), weil sie dem Konto nicht gehören.
  Schon markierte bleiben, wie sie sind. Höchstens 500 Favoriten.
- `DELETE /api/v4/fylvo/favorites` mit `{"ids": ["hashid", ...]}`: Markierung weg. Unbekannte IDs
  werden ignoriert.

Gespeichert wird die interne Datei-ID mit Zeitpunkt. Umbenennen und Verschieben ändern nichts. Wird
eine Datei endgültig gelöscht, verschwindet ihr Favorit beim nächsten Lesen.

### Tags

`GET /api/v4/fylvo/tags` liefert `[{"name": "Wichtig", "color": "#e5484d", "count": 12}]`: alle Tags
an eigenen Dateien ausserhalb des Papierkorbs, nach Name sortiert. `color` ist leer, wenn der Tag
keine Farbe hat (bei verschiedenen Farben gewinnt die häufigste). Klick auf einen Tag öffnet die
bestehende Suche nach diesem Tag (gleiche Adresse wie heute beim Klick auf einen Tag in der Liste).
Tags setzen und Ordnerfarben setzen bleibt die bestehende Schnittstelle `PATCH /file/metadata`
(`tag:<Name>` mit Farbe als Wert, `customize:icon_color`).

### Akzentfarben

| id | dunkel | hell |
| --- | --- | --- |
| `indigo` (Standard) | `#7484F4` | `#5565E8` |
| `teal` | `#2EC4B6` | `#0F8C81` |
| `green` | `#4CC38A` | `#22875A` |
| `amber` | `#F5A524` | `#A86A00` |
| `coral` | `#F2735B` | `#C9452B` |
| `violet` | `#A78BFA` | `#7C4DDB` |

### Gleichzeitige Änderungen

`SaveSettings` schreibt heute das ganze JSON aus dem Benutzer der Anfrage. Neue Funktion im
`inventory`: die Fylvo-Felder werden unter einer Sperre pro Benutzer frisch aus der Datenbank
gelesen, geändert und gespeichert. `SaveSettings` (bestehende Aufrufe: Anheften, Ansicht,
Einstellungen) übernimmt die Fylvo-Felder vor dem Schreiben aus der Datenbank, damit eine ältere
Kopie sie nicht überschreibt.

### Anheften

`PUT/DELETE /file/pin` bleiben für alte Aufrufer bestehen und ändern ab jetzt die `link`-Einträge in
`sidebar` (beim ersten Mal wird die Standard-Seitenleiste mit den alten Einträgen gespeichert und
`pined` geleert). Die Web-App liest angeheftete Einträge nur noch aus `prefs`.

## Web-App

Etappe 1, Grundlage: Einstellungen vom Server laden (beim Start mit `site/basic`, dann im Redux),
lokale Kopie im Browser nur als Startwert gegen Flackern. Hell, dunkel und Akzentfarbe aus `prefs`
statt aus dem Browser. Bestehende Browser-Werte (dunkel, Standard-Ansicht, Sortierung) werden beim
ersten Laden einmal ans Konto übertragen, wenn dort noch nichts gespeichert ist.

Etappe 2, Seitenleiste: Darstellung aus `sidebar.items` (Ordnerbaum unter "Meine Dateien" bleibt,
einklappbar). Knopf "Anpassen" (Stift) öffnet einen Dialog: Einträge ziehen, Auge zum Ausblenden,
Verknüpfung hinzufügen (Ordner wählen, Tag wählen, aktuelle Suche), Überschrift hinzufügen,
Zurücksetzen. Die Seiten-Links unten (Freigaben, Aufgaben, Verbinden, Verwaltung) bleiben wie sie sind.

Etappe 3, Favoriten und Tags: Stern im Rechtsklick und in der Mehrfachauswahl ("Zu Favoriten",
"Aus Favoriten entfernen"), kleiner Stern in der Liste neben dem Namen. Seite `/favorites` als
schlichte Liste (Klick: Ordner öffnen oder Datei im Ordner zeigen). Tags in der Seitenleiste mit Farbe
und Anzahl, Klick öffnet die Suche. "Farbe" als eigener Eintrag im Rechtsklick (heute versteckt
unter "Organisieren").

Etappe 4, Startseite und Ansicht: Seite `/home` mit den Bausteinen aus `home.widgets` (Zuletzt
geändert, 12 Dateien wie am Handy; Favoriten; Verknüpfungen; Tags; Speicher). `start_page`
entscheidet, wohin `/` führt. Neue Einstellungsseite "Anpassen" unter Einstellungen: Startseite,
Bausteine, Dichte, Standard-Ansicht und Sortierung, hell/dunkel/wie Gerät, Akzentfarbe, Zurücksetzen.
Dichte ändert nur Zeilenhöhe und Abstände der Dateiliste und der Seitenleiste.

Jede Etappe ist für sich nutzbar und wird einzeln committet.

## Handy (Grundlage für den Android-Thread)

Liest `GET /fylvo/prefs` und benutzt `sidebar` (als Liste in "Dateien" bzw. Start), `home`,
`start_page` (womit die App startet), `view`, `appearance`, `favorites`. Legt Reiter und Wischgesten
in `mobile` ab. Favoriten über `/fylvo/favorites`, Tags über `/fylvo/tags`, Ordnerfarben und Tags
anzeigen aus `metadata` der Datei (`customize:icon_color`, `tag:*`) und setzen über `PATCH /file/metadata`.

## Prüfen

- Go-Tests: Standardwerte, Auffüllen, Prüfung ungültiger Werte, Teil-Änderung überschreibt andere
  Teile nicht, `SaveSettings` mit alter Kopie verliert Fylvo-Felder nicht, Favoriten (eigene Datei,
  umbenannt, verschoben, im Papierkorb, gemeinsamer Ordner als Mitglied, Mitgliedschaft entzogen,
  öffentlicher Link abgelehnt, endgültig gelöscht), Tags (nur eigene, ohne Papierkorb), alte
  angeheftete Einträge erscheinen als Verknüpfungen.
- `go build ./...`, `npx tsc --noEmit` (96 alte Fehler, keine neuen), Build der Web-App.
- Im Browser: hell, dunkel, Handybreite, ohne gespeicherte Einstellungen (sieht aus wie heute).
- Review mit `rat`.

## Nicht dabei

Eigene Farben frei wählbar, gespeicherte Suchen als eigene Funktion (geht als Verknüpfung), Menüs
sortieren, private Tags, Grösse der Vorschaubilder (gibt es schon als Regler in der Galerie).
