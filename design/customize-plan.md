# Fylvo: Anpassung (Seitenleiste, Favoriten, Tags, Startseite, Ansicht, Aussehen)

Stand 2026-11-22 (Fassung 2, nach Plan-Review Runde 1). Auftrag von Sven: mehr Anpassung auf Web
und Handy. Ideenkatalog mit Sven abgestimmt, alle vier Bündel gewählt.

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

- Ohne Anpassung sieht alles aus wie heute. Gespeichert wird nur, was jemand ändert; sonst gelten
  Standardwerte, die dem heutigen Verhalten entsprechen. Neue Einträge der Seitenleiste
  (Favoriten, Tags) erscheinen erst, wenn es Favoriten oder Tags gibt; "Start" ist anfangs aus.
- Der Server kennt die Standardwerte, die Apps nicht. `GET` liefert die wirksame Einstellung
  (gespeichert über Standard gelegt) und dazu `customized`: die Liste der Teile, die das Konto selbst
  gespeichert hat. So gibt es die Logik nur einmal.
- Keine neue Tabelle, keine Schema-Änderung: alles im JSON-Feld `settings` des Benutzers
  (`inventory/types.UserSetting`, neue Felder `fylvo_prefs` und `fylvo_favorites`).
- Rechte: nichts davon gibt Zugriff. Favoriten werden beim Lesen durch die normale Dateiverwaltung
  (`fm.Get` mit der Fähigkeit "Info") als der anfragende Benutzer geladen und mit dem normalen
  Serializer ausgegeben, nie das rohe Datenbankobjekt.
- Keine Übernahme alter Browser-Werte: was heute im Browser liegt (hell/dunkel), gilt dort weiter,
  bis das Konto selbst etwas gespeichert hat (`customized`).

## Schnittstelle am Server (neu, unter `/api/v4/fylvo`)

Alle Routen: angemeldet. Lesen von `prefs` braucht `UserInfo.Read`, Ändern `UserInfo.Write`.
`favorites` und `tags` lesen braucht zusätzlich `Files.Read`, Favoriten ändern `Files.Write`.
(`prefs` enthält Datei-IDs und Adressen, darum beim Lesen auch `Files.Read`.)

### Einstellungen

`GET /api/v4/fylvo/prefs` liefert (hier: Standard für ein Konto ohne Anpassung, mit einem alten
angehefteten Ordner):

```json
{
  "sidebar": {
    "items": [
      { "type": "home", "hidden": true },
      { "type": "my_files" },
      { "type": "favorites" },
      { "type": "tags" },
      { "type": "category", "category": "image" },
      { "type": "category", "category": "video" },
      { "type": "category", "category": "audio" },
      { "type": "category", "category": "document" },
      { "type": "shared_with_me" },
      { "type": "trash" },
      { "type": "link", "uri": "cloudreve://my/Steuern", "name": "Steuern" }
    ]
  },
  "home": {
    "widgets": [
      { "type": "recent" },
      { "type": "favorites" },
      { "type": "links" },
      { "type": "tags" },
      { "type": "storage" }
    ]
  },
  "start_page": "my_files",
  "view": { "density": "compact", "layout": "grid", "sort_by": "created_at", "sort_direction": "asc" },
  "appearance": { "mode": "system", "accent": "#7484F4" },
  "mobile": {},
  "favorites": ["hashid1", "hashid2"],
  "customized": []
}
```

- Werte:
  - `sidebar.items[].type`: `home`, `my_files`, `favorites`, `tags`, `shared_with_me`, `trash`,
    `category` (mit `category` = `image|video|audio|document`), `link` (mit `uri`, optional `name`),
    `heading` (mit `name`). `hidden` bei allen ausser `link` und `heading` (die werden gelöscht).
    Feste Typen höchstens einmal, `category` einmal pro Kategorie. `favorites` und `tags` zeigen die
    Apps nur, wenn es welche gibt. Neu gespeichert: höchstens 100 Einträge, Name höchstens 64
    Zeichen; alte angeheftete Einträge werden ohne diese Grenzen übernommen. Links: jede gültige
    Fylvo-Adresse (wie heute beim Anheften); eine Verknüpfung gibt keinen Zugriff, sie wird beim
    Öffnen normal geprüft.
  - `home.widgets[].type`: `recent`, `favorites`, `links` (Verknüpfungen der Seitenleiste), `tags`,
    `storage`; je höchstens einmal, `hidden` erlaubt.
  - `start_page`: `home` oder `my_files`.
  - `view.density`: `compact` (heute), `normal`, `comfortable`. `view.layout`: `list|grid|gallery`.
    `view.sort_by`: `name|size|updated_at|created_at`. `view.sort_direction`: `asc|desc`.
    Das ist die Ansicht für Ordner ohne eigene Ansicht (siehe "Ansicht" unten).
  - `appearance.mode`: `system|light|dark`. `appearance.accent`: ein Schlüssel aus den Farbthemen
    der Seite (`themes` in `site/basic`, Einstellung `theme_options`). Gespeichert wird die
    Akzentfarbe weiter im bestehenden Feld `preferred_theme`; `prefs` liest und schreibt dieses
    Feld, es gibt keine zweite Stelle.
  - `mobile`: freies JSON-Objekt für die Handy-App (Reiter, Wischgesten), höchstens 8 KB. Der Server
    prüft nur, dass es ein Objekt ist, und die Grösse. Das Format legt der Handy-Thread fest.
  - `favorites`: nur lesen, Hash-IDs der Favoriten (wie `id` in Datei-Antworten), neuester zuerst.
    Damit zeigen die Apps den Stern, ohne die Liste aufzulösen.
  - `customized`: nur lesen, Namen der gespeicherten Teile (`sidebar`, `home`, `start_page`, `view`,
    `appearance`, `mobile`).
- Standard ohne gespeicherte Seitenleiste: wie heute (Meine Dateien, vier Kategorien, Geteilt mit
  mir, Papierkorb), dazu `home` (aus), `favorites`, `tags`, und alle bestehenden angehefteten
  Einträge (`settings.pined`) als `link`. Eine gespeicherte Seitenleiste ergänzt der Server um feste
  Einträge, die darin fehlen (z. B. in einer späteren Version neu), am Schluss und versteckt.

`PATCH /api/v4/fylvo/prefs` mit einem Teil der obersten Felder, z. B. `{"sidebar": {...}}` oder
`{"appearance": {"mode": "dark"}}`. Jeder mitgeschickte Teil ersetzt den gespeicherten Teil ganz;
bei `view` und `appearance` werden fehlende Unterfelder mit dem wirksamen Wert gefüllt. `favorites`
und `customized` sind nicht schreibbar. Ungültige Werte: Fehler `40001`, nichts wird gespeichert.
Antwort wie `GET`. Zwei Geräte, die verschiedene Teile ändern, überschreiben sich nicht.

`DELETE /api/v4/fylvo/prefs` (Zurücksetzen) löscht alle gespeicherten Teile ausser `mobile`.
Verknüpfungen in der Seitenleiste bleiben dabei erhalten (sie wandern in die Standard-Seitenleiste),
Favoriten auch. Die Akzentfarbe geht auf den Standard der Seite zurück.

### Anheften (bestehend)

`settings.pined` wird nicht mehr geschrieben und nicht gelöscht (bei einem Zurückspielen einer alten
Version sind die alten Einträge noch da). Ist eine Seitenleiste gespeichert, ist sie massgebend.
`PUT/DELETE /file/pin` bleiben und ändern die `link`-Einträge der Seitenleiste (beim ersten Mal wird
die Standard-Seitenleiste mit den alten Einträgen gespeichert). `pined` in den Antworten von
`site/basic` und `user/setting` wird aus den wirksamen `link`-Einträgen gebildet, damit ältere
Aufrufer weiter stimmen.

### Favoriten

Gespeichert: `fylvo_favorites` = Liste `{id, at}` (interne Datei-ID, Zeitpunkt), höchstens 200.

Datei-ID zu Adresse (beim Lesen, eine Funktion im `dbfs`):

1. Datei per ID laden und zur Wurzel hochgehen (`findRoot`). Nicht gefunden: nicht verfügbar.
2. Wurzel ist die normale Wurzel des Benutzers selbst: `cloudreve://my/<Pfad>`.
3. Wurzel ist ein Papierkorb oder gehört jemand anderem: unter den Vorfahren nach einem Ordner
   suchen, der Wurzel einer gültigen Mitglieder-Freigabe ist, in der der Benutzer Mitglied ist
   (`ListForMember`, einmal pro Anfrage geladen). Gefunden: `cloudreve://<share>@share/<Pfad ab
   dort>`. Sonst: nicht verfügbar. (Die Wurzel des Besitzers muss dabei seine normale Wurzel sein,
   sonst liegt die Freigabe im Papierkorb.)
4. Die Adresse über `fm.Get` als Benutzer laden (mit "Info"-Fähigkeit) und prüfen, dass die
   geladene Datei dieselbe ID hat. Sonst: nicht verfügbar.

Ein Administrator bekommt dabei keine Sonderrechte (nicht `TraverseFile` benutzen).

- `GET /api/v4/fylvo/favorites` liefert `{"files": [Datei-Antwort wie bei GET /file],
  "unavailable": 2}`, neuester zuerst. Der GET schreibt nichts; nicht verfügbare werden nur
  gezählt. Beim nächsten Schreiben der Favoriten fallen endgültig gelöschte Dateien heraus.
- `PUT /api/v4/fylvo/favorites` mit `{"uris": [...]}`. Angenommen:
  - `cloudreve://my/...` (keine Suche, nicht die Wurzel),
  - `cloudreve://<share>@share/...` einer Mitglieder-Freigabe, in der man Mitglied ist (auch ihre Wurzel),
  - Einträge aus "Geteilt mit mir" (`cloudreve://shared_with_me/<id>`): werden über ihr Ziel
    (`sys:shared_redirect`) aufgelöst; zeigt das auf einen öffentlichen Link, wird abgelehnt.
  - Abgelehnt (`40001`): Papierkorb, Suchen, öffentliche Freigabe-Links, alles, was nicht über die
    vier Schritte oben wieder zur selben Datei führt.
  Bereits markierte bleiben. Über 200: Fehler.
- `DELETE /api/v4/fylvo/favorites` mit `{"ids": [...]}`. Unbekannte IDs werden ignoriert.

### Tags

`GET /api/v4/fylvo/tags` liefert `[{"name": "Wichtig", "color": "#e5484d", "count": 12}]` nach Name.
Gezählt werden eigene Dateien, deren Wurzel die normale Wurzel ist (also auch nicht in einem Ordner
im Papierkorb). Die Prüfung der Wurzel merkt sich bereits geprüfte Ordner. `color` ist die häufigste
Farbe, leer wenn keine. Klick öffnet die bestehende Tag-Suche (gleiche Adresse wie beim Klick auf
einen Tag in der Dateiliste heute). Setzen bleibt `PATCH /file/metadata`.

### Ansicht

Ordner ohne eigene Ansicht (auch nicht von einem übergeordneten Ordner geerbt) bekommen heute vom
Server eine feste Standard-Ansicht (`getDefaultView`, Raster). Neu: hat der Besitzer `view`
gespeichert, nimmt der Server dessen Werte statt der festen. Ohne gespeicherte `view` bleibt alles
wie heute. In einem gemeinsamen Ordner gilt (wie bei der Ansicht pro Ordner) die des Besitzers.
Die Dichte gilt nur in den Apps und immer die eigene.

### Akzentfarben

`theme_options` bekommt sechs Einträge (Schlüssel = Farbe im dunklen Modus):
`#7484F4` (Standard, wie heute), `#2EC4B6`, `#4CC38A`, `#F5A524`, `#F2735B`, `#A78BFA`,
je mit passender helleren Variante für den hellen Modus und sonst den heutigen Fylvo-Flächen.
Bestehende Installationen: beim Start wird `theme_options` nur dann ersetzt, wenn der gespeicherte
Wert genau der heutige (eine Farbe) ist; selbst geänderte Werte bleiben.

### Gleichzeitige Änderungen

Neue Funktion `UpdateSettings(ctx, uid, change func(*types.UserSetting) error)` im `inventory`: eine
Sperre im Prozess (ein Server, SQLite), Zeile frisch lesen, nur die gewünschte Änderung anwenden,
schreiben. Alle Schreiber des Felds laufen darüber: Anheften, Ansicht (`operation.go`),
`PATCH /user/setting`, Passwortwechsel (bestehende Bedingung bleibt), Admin-Änderung eines Benutzers
(übernimmt Fylvo-Felder aus der frischen Zeile), die neuen Routen. Test: Einstellung speichern
während Admin das Passwort zurücksetzt, beides bleibt.

## Web-App

Etappe 1, Grundlage: `prefs` laden (nach Anmeldung und beim Start, im Redux, Kopie im Browser nur
gegen Flackern). Hell/dunkel aus `prefs`, wenn `appearance` gespeichert ist, sonst wie heute aus dem
Browser; die Umschaltung speichert ab jetzt am Konto. Akzentfarbe: bestehende Auswahl in den
Einstellungen zeigt nun sechs Farben.

Etappe 2, Seitenleiste: Darstellung aus `sidebar.items` (Ordnerbaum unter "Meine Dateien" bleibt).
Knopf "Anpassen" öffnet einen Dialog: Einträge ziehen, Auge zum Ausblenden, Verknüpfung hinzufügen
(Ordner wählen, Tag wählen), Überschrift hinzufügen, Zurücksetzen. Anheften im Menü wie bisher. Die
Seiten-Links darunter (Freigaben, Aufgaben, Verbinden, Verwaltung) bleiben.

Etappe 3, Favoriten und Tags: Stern im Rechtsklick und in der Mehrfachauswahl, kleiner Stern neben
dem Namen in der Liste. Seite `/favorites` als schlichte Liste (Klick: Ordner öffnen oder Datei im
Ordner zeigen). Tags in der Seitenleiste mit Farbe und Anzahl, Klick öffnet die Suche. "Farbe" als
eigener Eintrag im Rechtsklick für Ordner (heute unter "Organisieren").

Etappe 4, Startseite und Ansicht: Seite `/start` (`/home` ist die Dateiliste und bleibt es) mit den
Bausteinen aus `home.widgets` (Zuletzt geändert: 12 Dateien, gleiche Abfrage wie am Handy;
Favoriten; Verknüpfungen; Tags; Speicher). `start_page` gilt für `/` und nach der Anmeldung.
Einstellungsseite "Anpassen": Startseite, Bausteine, Dichte, Standard-Ansicht und Sortierung,
hell/dunkel/wie Gerät, Akzentfarbe, Zurücksetzen. Dichte ändert Zeilenhöhe und Abstände der
Dateiliste und der Seitenleiste.

Jede Etappe ist für sich nutzbar und wird einzeln committet.

## Handy (Grundlage für den Android-Thread)

Liest `GET /fylvo/prefs` und benutzt `sidebar` (als Liste von Orten), `home`, `start_page`, `view`
(Dichte; Ansicht und Sortierung kommen pro Ordner schon vom Server), `appearance`, `favorites`.
Legt Reiter und Wischgesten in `mobile` ab. Favoriten über `/fylvo/favorites`, Tags über
`/fylvo/tags`, Ordnerfarben und Tags aus `metadata` der Datei (`customize:icon_color`, `tag:*`),
setzen über `PATCH /file/metadata`. Akzentfarben aus `site/basic` `themes`.

## Prüfen

- Go-Tests: Standardwerte ohne gespeicherte Daten (wie heute), Auffüllen, ungültige Werte, Teil-
  Änderung lässt andere Teile stehen, Schreiber laufen über `UpdateSettings` (Admin-Änderung und
  Passwortwechsel verlieren nichts), alte angeheftete Einträge erscheinen als Verknüpfungen und
  `pined` bleibt in den Antworten, Favoriten (eigene Datei, umbenannt, verschoben, im Papierkorb,
  Ordner darüber im Papierkorb, gemeinsamer Ordner als Mitglied und dessen Wurzel, Mitgliedschaft
  entzogen, aus der Freigabe herausverschoben, öffentlicher Link abgelehnt, "Geteilt mit mir",
  endgültig gelöscht, Admin sieht fremde Dateien nicht), Tags (nur eigene, ohne Papierkorb und ohne
  Inhalt von Ordnern im Papierkorb), Ansicht-Standard des Besitzers, Scopes der Routen,
  `theme_options` wird nur ersetzt, wenn unverändert.
- `go build ./...`, `npx tsc --noEmit` (96 alte Fehler, keine neuen), Build der Web-App.
- Im Browser: hell, dunkel, Handybreite, ein Konto ohne Anpassung sieht aus wie heute.
- Review mit `rat`.

## Nicht dabei

Eigene Farben frei wählbar, gespeicherte Suchen als eigene Funktion (geht als Verknüpfung), Menüs
sortieren, private Tags, Grösse der Vorschaubilder (Regler in der Galerie gibt es schon).
