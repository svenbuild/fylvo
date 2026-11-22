# Fylvo: Anpassung (Seitenleiste, Favoriten, Tags, Startseite, Ansicht, Aussehen)

Stand 2026-11-22 (Fassung 3, nach Plan-Review Runde 2). Auftrag von Sven: mehr Anpassung auf Web
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

Alle Routen: angemeldet und `Files.Read` (alle Antworten enthalten Datei-IDs oder Adressen).
Dazu: `prefs` lesen `UserInfo.Read`, ändern `UserInfo.Write`; Favoriten ändern `Files.Write`.
Die Verwaltung (Admin) sieht die neuen Felder nicht: `fylvo_prefs` und `fylvo_favorites` werden aus
den Benutzer-Antworten der Verwaltung entfernt (wie Passwörter). Der Admin kann aber wie heute alle
Dateien sehen.

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
  "start_page": "",
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
  - `start_page`: `""` (Standard: jede App wie heute, Web in "Meine Dateien", Handy auf "Home"),
    `home` oder `my_files`.
  - `view.density`: `compact` (heute), `normal`, `comfortable`; gespeichert in `fylvo_prefs`, gilt
    nur in den Apps. `view.layout` (`list|grid|gallery`), `view.sort_by`
    (`name|size|updated_at|created_at`), `view.sort_direction` (`asc|desc`): siehe "Ansicht" unten.
  - `appearance.mode`: `system|light|dark`, gespeichert in `fylvo_prefs`.
    `appearance.accent`: ein Schlüssel aus den Farbthemen der Seite (`themes` in `site/basic`,
    Einstellung `theme_options`), gespeichert nur im bestehenden Feld `preferred_theme`. Ist dort
    nichts oder ein unbekannter Schlüssel, liefert `GET` den Standard der Seite. Beide Felder sind
    unabhängig: wer nur die Farbe ändert, speichert keinen Modus.
  - `mobile`: freies JSON-Objekt für die Handy-App (Reiter, Wischgesten), höchstens 8 KB. Der Server
    prüft nur, dass es ein Objekt ist, und die Grösse. Das Format legt der Handy-Thread fest.
  - `favorites`: nur lesen, Hash-IDs der Favoriten (wie `id` in Datei-Antworten), neuester zuerst.
    Damit zeigen die Apps den Stern, ohne die Liste aufzulösen.
  - `customized`: nur lesen, Namen der gespeicherten Teile (`sidebar`, `home`, `start_page`,
    `density`, `mode`, `mobile`). Die Web-App nimmt hell/dunkel nur dann vom Konto, wenn `mode`
    darin steht, sonst wie heute aus dem Browser.
- Standard ohne gespeicherte Seitenleiste: wie heute (Meine Dateien, vier Kategorien, Geteilt mit
  mir, Papierkorb), dazu `home` (aus), `favorites`, `tags`, und alle bestehenden angehefteten
  Einträge (`settings.pined`) als `link`. Eine gespeicherte Seitenleiste ergänzt der Server um feste
  Einträge, die darin fehlen (z. B. in einer späteren Version neu), am Schluss und versteckt.

`PATCH /api/v4/fylvo/prefs` mit einem Teil der obersten Felder, z. B. `{"sidebar": {...}}` oder
`{"appearance": {"mode": "dark"}}`. `sidebar`, `home`, `start_page`, `mobile` ersetzen den
gespeicherten Teil ganz. Bei `view` und `appearance` wird nur geschrieben, was mitgeschickt ist
(jedes Unterfeld einzeln). `favorites` und `customized` sind nicht schreibbar. Ungültige Werte: Fehler `40001`, nichts wird gespeichert.
Antwort wie `GET`. Zwei Geräte, die verschiedene Teile ändern, überschreiben sich nicht.

`DELETE /api/v4/fylvo/prefs` (Zurücksetzen) löscht alle in `fylvo_prefs` gespeicherten Teile ausser
`mobile` und leert `preferred_theme`. Verknüpfungen bleiben (sie stehen immer auch in `pined`, siehe
unten), Favoriten und die Ansicht der Ordner auch.

### Anheften (bestehend)

`settings.pined` bleibt die Liste der Verknüpfungen und ist immer aktuell: jedes Speichern der
Seitenleiste schreibt die `link`-Einträge (in ihrer Reihenfolge) auch nach `pined`. So stimmen alte
Aufrufer (`BuildUser`: Anmeldung, `user/me`, `site/basic`), ein Zurückspielen einer alten Version
und das Zurücksetzen. Beim Lesen einer gespeicherten Seitenleiste gleicht der Server mit `pined` ab:
Links, die in `pined` fehlen, fallen weg, neue aus `pined` kommen ans Ende.
`PUT/DELETE /file/pin` bleiben, wie sie sind (ändern `pined`, Scope `Files.Write`), und wirken damit
auch auf die Seitenleiste.

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
4. Die Adresse über `fm.Get` als Benutzer laden (öffentliche Metadaten, Fähigkeit "Info", die
   Wurzel einer Freigabe erlaubt) und prüfen, dass die geladene Datei dieselbe ID hat. Sonst: nicht
   verfügbar.

Ein Administrator bekommt dabei keine Sonderrechte (nicht `TraverseFile` benutzen). Bereits geprüfte
Ordner merkt sich eine Anfrage, damit 200 Favoriten nicht 200 Mal denselben Weg hochgehen.

- `GET /api/v4/fylvo/favorites` liefert `{"files": [Datei-Antwort wie bei GET /file],
  "unavailable": 2}`, neuester zuerst. Der GET schreibt nichts; nicht verfügbare werden nur
  gezählt. Beim nächsten Schreiben der Favoriten fallen endgültig gelöschte Dateien heraus.
- `PUT /api/v4/fylvo/favorites` mit `{"uris": [...]}`. Angenommen:
  - `cloudreve://my/...` (keine Suche, nicht die Wurzel),
  - `cloudreve://<share>@share/...` einer Mitglieder-Freigabe, in der man Mitglied ist (auch ihre Wurzel),
  - Abgelehnt (`40001`): `shared_with_me`-Adressen (die Apps schicken für Einträge aus "Geteilt
    mit mir" deren Ziel aus `metadata["sys:shared_redirect"]`), Papierkorb, Suchen, öffentliche
    Freigabe-Links, alles, was nicht über die vier Schritte oben wieder zur selben Datei führt.
  Bereits markierte bleiben. Über 200: Fehler.
- `DELETE /api/v4/fylvo/favorites` mit `{"ids": [...]}`: Hash-IDs wie in `prefs.favorites`.
  Unbekannte werden ignoriert.

### Tags

`GET /api/v4/fylvo/tags` liefert `[{"name": "Wichtig", "color": "#e5484d", "count": 12}]` nach Name.
Gezählt werden eigene Dateien, deren Wurzel die normale Wurzel ist (also auch nicht in einem Ordner
im Papierkorb). Die Prüfung der Wurzel merkt sich bereits geprüfte Ordner. `color` ist die häufigste
Farbe, leer wenn keine. Klick öffnet eine Suche nur nach dem Namen ab "Meine Dateien" (ohne Farbe,
mit dem bestehenden Such-Helfer der Web-App), damit Anzahl und Ergebnis zusammenpassen. Setzen
bleibt `PATCH /file/metadata`.

### Ansicht

Kein neuer Mechanismus: Ordner erben ihre Ansicht schon heute vom nächsten übergeordneten Ordner
mit eigener Ansicht, sonst gilt die feste Standard-Ansicht (`getDefaultView`, Raster). Die
"Standard-Ansicht" in `prefs.view` ist darum die Ansicht der Wurzel "Meine Dateien" (`Props.View`
des Wurzelordners): `GET` liest sie (fehlt sie, die feste), `PATCH` schreibt sie über den
bestehenden Weg `PatchView` (immer ein neues Objekt; `PageSize`, `Thumbnail`, Spalten und
Galeriebreite bleiben). Ordner mit eigener Ansicht behalten sie. Am Handy (Android-Thread): die App
schickt heute ihre eigene Sortierung mit; will sie der Konto-Ansicht folgen, lässt sie diese weg und
liest `view` aus der Antwort der Liste.

### Akzentfarben

`theme_options` bekommt sechs Einträge (Schlüssel = Farbe im dunklen Modus):
`#7484F4` (Standard, wie heute), `#2EC4B6`, `#4CC38A`, `#F5A524`, `#F2735B`, `#A78BFA`,
je mit passender helleren Variante für den hellen Modus und sonst den heutigen Fylvo-Flächen.
Bestehende Installationen: beim Start wird `theme_options` nur dann ersetzt, wenn der gespeicherte
Wert als JSON gleich dem heutigen (eine Farbe) ist; selbst geänderte Werte bleiben.

### Gleichzeitige Änderungen

Eine Sperre im Prozess für das Feld `settings` (ein Server, SQLite). Neue Funktion
`UpdateSettings(ctx, uid, change func(*types.UserSetting) error)`: Sperre, Zeile frisch lesen, nur
die gewünschte Änderung anwenden, schreiben. Darüber laufen Anheften, Ansicht der Bereiche
Papierkorb und Geteilt (`operation.go`), `PATCH /user/setting` und die neuen Routen.
Die zwei Stellen, die Passwort und Einstellungen in einer Anweisung schreiben, nehmen dieselbe Sperre
und lesen die Einstellungen darin frisch: `SetOwnPassword` (Bedingung "altes Passwort" bleibt) und
die Admin-Änderung (`Upsert` mit `passwordChangeSettings`; sie übernimmt alles ausser den Feldern, die
der Admin ändert, aus der frischen Zeile, also auch `fylvo_prefs`, `fylvo_favorites`, `pined`).
Test: Einstellung speichern während Admin das Passwort zurücksetzt, beides bleibt.

## Web-App

Etappe 1, Grundlage: `prefs` laden (nach Anmeldung und beim Start, im Redux, Kopie im Browser nur
gegen Flackern). Hell/dunkel aus `prefs`, wenn `mode` in `customized` steht, sonst wie heute aus dem
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

Liest `GET /fylvo/prefs` und benutzt `sidebar` (als Liste von Orten), `home`, `start_page` (leer:
wie heute auf "Home"), `view` (siehe "Ansicht"), `appearance`, `favorites`.
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
