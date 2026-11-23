# Fylvo: Anpassung (Seitenleiste, Favoriten, Tags, Startseite, Ansicht, Aussehen)

Stand 2026-11-23 (gebaut: Server und Web-App). Ziel: mehr Anpassung auf Web und Handy. Diese Datei ist
die gültige Beschreibung; die Endfassung der Schnittstelle für das Handy steht im Abschnitt
"Für die Android-App".

## Entscheide (2026-11-22)

1. Gebaut wird: Seitenleiste frei zusammenstellen, Favoriten (Stern), Farben und Tags sichtbar
   machen, Startseite, Ansicht (Dichte, Standard-Ansicht und Sortierung), Aussehen (hell, dunkel,
   wie Gerät, Akzentfarbe).
2. Anpassungen hängen am **Konto** und sind auf Web, Handy und später Desktop gleich. Nur was vom
   Bildschirm abhängt (Breite der Seitenleiste, Ordnerbaum auf- und zugeklappt) bleibt pro Gerät.
3. Tags und Ordnerfarben bleiben wie heute an der Datei und für Freigabe-Empfänger sichtbar.
   Favoriten sind privat.
4. Handy (wird separat gebaut, auf dieser Schnittstelle): Reiter unten wählbar und sortierbar,
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
Dazu: `prefs` lesen `UserInfo.Read`; `prefs` ändern `UserInfo.Write` und `Files.Write` (es ändert
Verknüpfungen, wie `/file/pin`); Favoriten ändern `Files.Write`.
Favoriten sind privat gegenüber anderen Benutzern und Freigabe-Empfängern. Die Verwaltung (Admin)
kann sie wie heute `pined` und alle anderen Kontodaten in den rohen Benutzerdaten sehen; er sieht
ohnehin alle Dateien. Das bleibt bewusst so (kein Ausblenden in allen Admin-Antworten).

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
      { "type": "link", "uri": "cloudreve://my/Projekte", "name": "Projekte" }
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
    `heading` (mit `name`), `connected` (mit `id`, ein verbundener Speicher, siehe unten).
    `hidden` bei allen ausser `link` und `heading` (die werden gelöscht).
    Feste Typen höchstens einmal, `category` einmal pro Kategorie. `favorites` und `tags` zeigen die
    Apps nur, wenn es welche gibt. Höchstens 100 Einträge (gezählt wie angezeigt, mit den festen
    Einträgen, die der Server ergänzt), oder so viele, wie die Seitenleiste schon hatte (alte grosse
    Anheft-Listen). Name höchstens 64 Zeichen; ein Link, der unverändert schon in `pined` steht, darf
    länger sein, damit Verschieben und Ausblenden immer gehen. Links: jede gültige
    Fylvo-Adresse (wie heute beim Anheften), normalisiert wie dort (`uri.String()`), jede Adresse
    nur einmal; eine Verknüpfung gibt keinen Zugriff, sie wird beim Öffnen normal geprüft.
  - Verbundene Speicher (kommen später): der Server bekommt eine Stelle, die die verbundenen
    Speicher des Kontos liefert (heute leer). Beim Lesen gleicht er die Seitenleiste damit ab: neue
    Speicher kommen sichtbar ans Ende, entfernte fallen weg, sonst bleiben Reihenfolge und `hidden`
    wie gespeichert. Beim Speichern fallen `connected`-Einträge mit unbekannter `id` still weg.
    Reiter und Startseite am Handy gleicht die Handy-App selbst ab (der Server liest `mobile` nicht).
  - `home.widgets[].type`: `recent`, `favorites`, `links` (Verknüpfungen der Seitenleiste), `tags`,
    `storage`; je höchstens einmal, `hidden` erlaubt.
  - `start_page`: `""` (Standard: jede App wie heute, Web in "Meine Dateien", Handy auf "Home"),
    `home` oder `my_files`.
  - `view.density`: `compact` (heute), `normal`, `comfortable`; gespeichert in `fylvo_prefs`, gilt
    nur in den Apps, das einzige schreibbare Feld von `view`. `view.layout` (`list|grid|gallery`),
    `view.sort_by` (`name|size|updated_at|created_at`), `view.sort_direction` (`asc|desc`): nur
    lesen (im `PATCH`: Fehler `40001`), siehe "Ansicht" unten. Ist in der gespeicherten Ansicht keine
    Sortierung gesetzt, meldet `GET` `created_at`; die Liste ordnet dann nach der Reihenfolge des
    Anlegens (praktisch gleich).
  - `appearance.mode`: `system|light|dark`, gespeichert in `fylvo_prefs`.
    `appearance.accent`: ein Schlüssel aus den Farbthemen der Seite (`themes` in `site/basic`,
    Einstellung `theme_options`), gespeichert nur im bestehenden Feld `preferred_theme`. Ist dort
    nichts oder ein unbekannter Schlüssel, liefert `GET` den Standard der Seite. Beide Felder sind
    unabhängig: wer nur die Farbe ändert, speichert keinen Modus.
  - `mobile`: freies JSON-Objekt für die Handy-App (Reiter, Wischgesten), höchstens 8 KB. Der Server
    prüft nur, dass es ein Objekt ist, und die Grösse. Das Format legt die Handy-App fest.
  - `favorites`: nur lesen, Hash-IDs der Favoriten (wie `id` in Datei-Antworten), neuester zuerst.
    Damit zeigen die Apps den Stern, ohne die Liste aufzulösen.
  - `customized`: nur lesen, Namen der gespeicherten Teile (`sidebar`, `home`, `start_page`,
    `density`, `mode`, `mobile`, und `view`, wenn die Wurzel "Meine Dateien" eine eigene Ansicht
    hat). Die Web-App nimmt hell/dunkel nur dann vom Konto, wenn `mode`
    darin steht, sonst wie heute aus dem Browser.
- Standard ohne gespeicherte Seitenleiste: wie heute (Meine Dateien, vier Kategorien, Geteilt mit
  mir, Papierkorb), dazu `home` (aus), `favorites`, `tags`, und alle bestehenden angehefteten
  Einträge (`settings.pined`) als `link`. Eine gespeicherte Seitenleiste ergänzt der Server um feste
  Einträge, die darin fehlen (z. B. in einer späteren Version neu), am Schluss und versteckt.

`PATCH /api/v4/fylvo/prefs` mit einem Teil der obersten Felder, z. B. `{"sidebar": {...}}` oder
`{"appearance": {"mode": "dark"}}`. `sidebar`, `home`, `start_page`, `mobile` ersetzen den
gespeicherten Teil ganz. `sidebar` braucht `{"items": [...]}`, `home` braucht `{"widgets": [...]}`
(fehlt die Liste: `40001`); eine leere Liste bleibt leer gespeichert. Zu `sidebar` kann die App
`"base": ["<Adresse>", ...]` mitschicken, die Links, mit denen der Editor begonnen hat: ein
angehefteter Link, der weder in `items` noch in `base` steht (inzwischen auf einem anderen Gerät
angeheftet), bleibt dann am Ende erhalten. Ohne `base` gilt `items` genau so, wie geschickt. `{"sidebar": null}` und
`{"home": null}` setzen diesen Teil auf den Standard zurück (Verknüpfungen bleiben), `{"mobile":
null}` löscht `mobile`. Bei `view` (nur `density`) und `appearance` wird nur geschrieben, was
mitgeschickt ist (jedes Unterfeld einzeln). `favorites` und `customized` sind nicht schreibbar.
Erst wird alles geprüft, dann in einem Schreibvorgang gespeichert (Benutzerzeile, unter der Sperre). Ungültige Werte: Fehler `40001`, nichts wird gespeichert.
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

Datei-ID zu Adresse (beim Lesen, in `service/fylvo/fylvo_favorites.go`):

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
  - `cloudreve://<share>@share/...` nur, wenn diese Freigabe eine Mitglieder-Freigabe ist, in der
    man Mitglied ist (auch ihre Wurzel). Der Besitzer markiert seine Dateien über `my`.
  - Abgelehnt (`40001`): `shared_with_me`-Adressen, öffentliche Freigabe-Links (auch wenn man die
    Datei sonst erreichen könnte), Papierkorb, Suchadressen (eine gefundene Datei hat ihre normale
    Adresse und geht), alles, was nicht über die vier Schritte oben wieder zur selben Datei führt.
  Bereits markierte bleiben. Über 200: Fehler.
- `DELETE /api/v4/fylvo/favorites` mit `{"ids": [...]}`: Hash-IDs wie in `prefs.favorites`.
  Unbekannte werden ignoriert.
- `PUT` und `DELETE` antworten mit `{"favorites": ["<hashid>", ...]}` (neuester zuerst), nicht mit
  der ganzen Einstellung.

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
bestehenden Weg: `PATCH /file/view` auf `cloudreve://my`. Dafür holt die App zuerst die ganze
Ansicht aus einer Liste von `cloudreve://my` (ohne `page_size` und Sortierung in der Anfrage, sonst
überschreiben diese die gespeicherten Werte), ändert nur Darstellung und Sortierung und schickt das
ganze Objekt. Fehlende Felder liest sie wie die Web-App heute: fehlendes `thumbnail` heisst aus,
fehlende `gallery_width` und Spalten kommen aus den Browser-Werten der Web-App, fehlendes
`order_direction` ist `asc`, ein leeres `order` bleibt leer, wenn die Sortierung nicht geändert wird. `prefs` schreibt die Ansicht
nicht selbst, damit es nur einen Schreiber gibt. Ordner mit eigener Ansicht behalten sie. Hat das
Konto `disable_view_sync` an, enthält `view` nur `density`, `view` steht nicht in `customized`, und
die Einstellungsseite zeigt die Auswahl nicht. Am Handy: die App behält ihre heutige Ansicht und
Sortierung, solange `view` nicht in `customized` steht; sonst lässt sie bei normalen Ordnern unter
"Meine Dateien" ihre Sortierung in der Liste weg und folgt dem `view` aus der Antwort. Kategorien,
Suche und Freigaben (die oft kein `view` liefern) behalten ihre heutige Sortierung.

### Akzentfarben

`theme_options` bekommt sechs Einträge (Schlüssel = Farbe im dunklen Modus):
`#7484F4` (Standard, wie heute), `#2EC4B6`, `#4CC38A`, `#F5A524`, `#F2735B`, `#A78BFA`,
je mit passender helleren Variante für den hellen Modus und sonst den heutigen Fylvo-Flächen.
Bestehende Installationen: beim Start wird `theme_options` nur dann ersetzt, wenn der gespeicherte
Wert als JSON gleich einem bekannten alten Fylvo-Standard ist (der heutige und der erste
"navy"-Entwurf vor Commit `869204a3`); selbst geänderte Werte bleiben. Wenn das bei einer Instanz
nicht greift, zeigt die Auswahl einfach weiter eine Farbe (kein Fehler); der Betreiber kann den
Wert dann in der Verwaltung ersetzen.

### Gleichzeitige Änderungen

Eine Sperre im Prozess für das Feld `settings` (ein Server, SQLite). Neue Funktion
`UpdateSettings(ctx, uid, change func(*types.UserSetting) error)`: Sperre, Zeile frisch lesen, nur
die gewünschte Änderung anwenden, schreiben. Darüber laufen Anheften, Ansicht der Bereiche
Papierkorb und Geteilt (`operation.go`), `PATCH /user/setting` und die neuen Routen. Die Sperre ist
eine für alle Benutzer (einfach, die Schreibvorgänge sind kurz). Langsame Prüfungen (z. B. die
Adressen beim Markieren von Favoriten) laufen vor der Sperre; in der Sperre werden nur Doppelte gegen die frisch gelesene Liste entfernt,
die Grenze von 200 geprüft und angehängt.
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
dem Namen in der Liste. Kein Stern in der Liste "Geteilt mit mir" (ihre Einträge haben teils eine
andere ID als der Ordner, auf den sie zeigen), im Papierkorb, in öffentlichen Freigabe-Links und
für den Besitzer in seiner eigenen Freigabe.
Einen gemeinsamen Ordner selbst markiert man in ihm über das Menü des aktuellen Ordners; darin
funktioniert der Stern normal. Seite `/favorites` als schlichte Liste (Klick: Ordner öffnen oder Datei im
Ordner zeigen). Tags in der Seitenleiste mit Farbe und Anzahl, Klick öffnet die Suche. "Farbe" als
eigener Eintrag im Rechtsklick für Ordner (heute unter "Organisieren").

Etappe 4, Startseite und Ansicht: Seite `/start` (`/home` ist die Dateiliste und bleibt es) mit den
Bausteinen aus `home.widgets` (Zuletzt geändert: 12 Dateien, gleiche Abfrage wie am Handy;
Favoriten; Verknüpfungen; Tags; Speicher). `start_page` gilt für `/` und nach der Anmeldung (dafür
wartet die Anmeldung auf `prefs`; ein `redirect` in der Adresse geht vor).
Einstellungsseite "Anpassen": Startseite, Bausteine, Dichte, Standard-Ansicht und Sortierung,
hell/dunkel/wie Gerät, Akzentfarbe, Zurücksetzen. Dichte ändert Zeilenhöhe und Abstände der
Dateiliste und der Seitenleiste.

Jede Etappe ist für sich nutzbar und wird einzeln committet.

## Was an der Datenbankzeile einer Datei hängt (für verbundene Speicher)

- Favoriten: ja, gespeichert wird die Fylvo-Datei-ID. Dateien ohne eigene Zeile (z. B. in einem
  verbundenen Speicher) können so nicht Favorit sein. Der Stern fehlt dort; ein Ordner eines
  verbundenen Speichers lässt sich aber als Verknüpfung (`link` mit seiner Adresse) in die
  Seitenleiste legen.
- Tags und Ordnerfarben: ja, sie sind Metadaten der Datei (`tag:*`, `customize:icon_color`) in der
  Fylvo-Datenbank. In verbundenen Speichern gibt es sie nicht, und `/fylvo/tags` zählt sie nicht.
- Seitenleiste, Startseite, Ansicht, Aussehen: hängen am Konto, gehen überall.

## Für die Android-App

Endfassung der Schnittstelle, gebaut und getestet. Alle Routen unter `/api/v4/fylvo`, angemeldet,
Fehler im üblichen Umschlag (`code` != 0); ungültige Eingaben geben `40001`, die App zeigt dann die
Meldung und behält den alten Stand.

| Aufruf | Zweck | Antwort |
| --- | --- | --- |
| `GET prefs` | wirksame Einstellungen (siehe oben) | ganzes `prefs`-Objekt |
| `PATCH prefs` | Teile ändern (`sidebar`, `home`, `start_page`, `view.density`, `appearance.mode`, `appearance.accent`, `mobile`) | ganzes `prefs`-Objekt |
| `DELETE prefs` | alles ausser `mobile` zurücksetzen | ganzes `prefs`-Objekt |
| `GET favorites` | Favoriten als Datei-Antworten | `{"files": [...], "unavailable": n}` |
| `PUT favorites` `{"uris": [...]}` | markieren | `{"favorites": [...]}` |
| `DELETE favorites` `{"ids": [...]}` | Markierung weg | `{"favorites": [...]}` |
| `GET tags` | eigene Tags mit Farbe und Anzahl | `[{"name", "color", "count"}]` |

Dazu bestehend: Tags und Ordnerfarbe setzen mit `PATCH /api/v4/file/metadata` (`tag:<Name>` mit der
Farbe als Wert, `customize:icon_color`), die Standard-Ansicht mit `PATCH /api/v4/file/view` auf
`cloudreve://my` (siehe "Ansicht"), die Akzentfarben aus `GET /api/v4/site/config/basic` (`themes`,
Schlüssel = Farbe im dunklen Modus; die helle Variante steht im Objekt).

Gewählt für das Handy:

- Reiter unten wählbar und sortierbar.
- Wischgesten in Listen, links und rechts frei belegbar.
- Schnellzugriffe am App-Symbol (lange drücken) und ein Widget.
- Teilen in Fylvo mit Wahl des Zielordners statt immer "Meine Dateien".

Empfohlenes Format für `mobile` (der Server prüft nur Objekt und Grösse, die App entscheidet):

```json
{
  "v": 1,
  "tabs": ["home", "files", "favorites", "shared", "account"],
  "swipe": { "left": "delete", "right": "favorite" }
}
```

- `tabs`: Reihenfolge der Reiter, aus `home`, `files`, `favorites`, `shared`, `tags`, `account`;
  `account` ist immer dabei (sonst kommt man nicht an die Einstellungen), 3 bis 5 Reiter.
- `swipe.left` und `swipe.right`: `none`, `favorite`, `share`, `offline`, `delete`.
- Fehlt `mobile` oder ein Feld darin, gilt das heutige Verhalten (Reiter Home, Files, Shared,
  Account; keine Wischgesten). Unbekannte Werte überspringt die App; ein Schreiben ersetzt
  `mobile` ganz, darum immer das ganze Objekt schicken.

Was am Handy dem Konto folgt: Startseite (`start_page`; leer heisst wie heute "Home"), Bausteine der
Startseite (`home`), die Orte der Seitenleiste als Liste in "Files" oder im Menü (`sidebar`;
versteckte weglassen, `favorites` und `tags` nur wenn es welche gibt, unbekannte Typen wie
`connected` überspringen, bis die App sie kennt), Dichte (`view.density`), hell/dunkel (nur wenn
`mode` in `customized` steht, sonst wie das Gerät), Akzentfarbe, Favoriten (Stern), Standard-Ansicht
(nur wenn `view` in `customized` steht, siehe "Ansicht"), Reiter und Wischgesten (`mobile`).

Was pro Gerät bleibt: Offline-Ordner und ihre Einstellungen, Ordner-Abgleich, Sprache der App,
Benachrichtigungen, die Einrichtung des Widgets und der Schnellzugriffe (Android legt sie pro Gerät
an), der zuletzt gewählte Zielordner beim Teilen.

Stern am Handy: wie in der Web-App nur bei eigenen Dateien (`my`, auch in Suchergebnissen, die
ja ihre normale Adresse haben) und in Mitglieder-Freigaben, in denen man Mitglied ist. Nicht für
den Besitzer in seiner eigenen Freigabe (er markiert über `my`), nicht in "Geteilt mit mir", im
Papierkorb, auf einer Suchadresse selbst, in öffentlichen Links und nicht in verbundenen Speichern.
Ändert die App die Seitenleiste, schickt sie `base` mit (siehe `PATCH`).

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
- Hinweise für den Bau: Favorit auf der Wurzel einer Freigabe braucht die öffentlichen Metadaten
  ausdrücklich (die Wurzel wird beim Öffnen der Freigabe geladen, vor der Option). Tag-Suche aus der
  Seitenleiste immer ab `cloudreve://my`, auch wenn gerade der Papierkorb offen ist; die Suche kann
  bei sehr vielen Ordnern an die eingestellte Grenze stossen und weniger zeigen als die Anzahl.
- Code-Review.

## Nicht dabei

Eigene Farben frei wählbar, gespeicherte Suchen als eigene Funktion (geht als Verknüpfung), Menüs
sortieren, private Tags, Grösse der Vorschaubilder (Regler in der Galerie gibt es schon).
