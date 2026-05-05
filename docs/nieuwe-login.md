# Plan: Gedeelde QR Login Voor Koffie, Admin En Questionnaire

## Doel

We willen de demo versimpelen naar één gedeelde login-flow voor alles:

- koffie bestellen
- koffieconfig aanpassen
- questionnaire invullen

Iedereen die is ingelogd krijgt dezelfde rechten. Dat is een bewuste demo-keuze:

- de flow moet snel zijn
- het is acceptabel dat mensen de demo kunnen slopen
- via nickname willen we wel kunnen zien wie dat deed

## Gewenste gebruikersflow

## Stap 0: QR-code scannen

Op het scherm staat één QR-code. Die QR-code bevat al de huidige rollende code, zodat bezoekers die niet hoeven over te typen.

De QR-code landt op een URL zoals:

```text
/login?code=1234
```

of, als we meteen naar een verborgen questionnaire-flow willen:

```text
/login?code=1234&next=/answer/kubecon-2026
```

Belangrijk:

- de gebruiker hoeft de code niet zelf te onthouden of over te nemen
- de QR-code is alleen de toegangspoort
- de gebruiker krijgt daarna nog de kans om een nickname te kiezen

## Stap 1: nickname invullen

Na het scannen komt de gebruiker op een eenvoudige login-pagina met:

- nickname veld
- eventueel de code al verborgen ingevuld uit de URL (als de code er is natuurlijk)
- een knop zoals `Enter`
- de geldigheidsduur van de code gaat omhoog, naar 2 uur

De gebruiker hoeft dus alleen nog een naam te bedenken en te bevestigen.

Na succesvolle login krijgt de gebruiker één gedeelde sessiecookie.

## Stap 2: vrij navigeren

Na login mag de gebruiker zelf navigeren naar:

- `/` voor koffie
- `/admin` voor live config
- `/admin/orders` voor orderfeed

De questionnaire hoeft niet zichtbaar in het menu te staan. Die mag alleen bereikbaar zijn via:

- een directe redirect
- een gedeelde link
- een QR-code met `next=...`

Dat houdt de hoofd-UI simpel.

---

## Belangrijkste vereenvoudiging

## Eén rollende code voor alles

We willen de rollende codes loshalen van de questionnaire-status en van individuele sessies.

Dus:

- niet meer per `QuizSession` een join code in `status.joinCode`
- niet meer een aparte coffee-login en questionnaire-join
- niet meer meerdere codes tegelijk in de UX

In plaats daarvan komt er:

- één globale rollende code voor de hele demo

Die code geeft toegang tot:

- koffie
- admin
- questionnaire

De globale code is dus een tijdelijke demo-sleutel, geen inhoudelijke sessiekiezer.

## Gevolg

De code bepaalt niet meer direct welke questionnaire-sessie actief is.

De questionnaire-context moet daarom uit de URL of route komen, bijvoorbeeld:

- `/answer/kubecon-2026`
- `/answer/demo-2026`

De login bewijst dan alleen dat iemand “binnen” is. Welke questionnaire getoond wordt, komt uit de route.

Dat is eenvoudiger dan de huidige koppeling waarbij een code zowel auth als sessieresolutie doet.

---

## Architectuur

## 1. Eén gedeelde browser-session

We gaan naar één signed cookie voor alle browserflows.

Die cookie bevat minimaal:

- `nickname`
- `issuedAt`
- `expiresAt`
- eventueel een versieveld

De cookie hoeft geen questionnaire `sessionRef` meer te bevatten zolang de questionnaire-route zelf de sessienaam bevat.

Voordeel:

- één auth-toestand voor alles
- geen apart admin-cookie
- geen aparte participant-cookie
- geen aparte join-cookie voor questionnaire

## 2. Eén publiek login-endpoint

Nieuw hoofdendpoint:

- `POST /public/login`

Request body:

```json
{
  "nickname": "Simon",
  "code": "1234"
}
```

Backend doet:

1. nickname valideren
2. globale rollende code valideren
3. gedeelde sessiecookie zetten
4. `204 No Content` of vergelijkbare success teruggeven

## 3. Eén publiek session-endpoint

Frontend moet kunnen opvragen of iemand al ingelogd is.

Daarvoor:

- `GET /public/session`

Response:

```json
{
  "nickname": "Simon"
}
```

Dat endpoint wordt gebruikt door:

- route guards
- login-scherm
- admin schermen
- koffie-scherm

---

## Routing en UX

## Koffie op `/`

De koffieflow blijft de hoofdapp.

- `/` wordt de primaire bestemming na login
- dit is het scherm dat de meeste bezoekers zullen zien

Als iemand niet is ingelogd en `/` opent:

- redirect naar `/login`
- na succesvolle login terug naar `/`

## Questionnaire niet in het menu

De questionnaire willen we niet promoten in de hoofdnavigation.

Dus:

- geen zichtbare menu-entry naar questionnaire
- geen knop in de admin-nav
- alleen directe toegang via URL

Voorbeeld:

- QR-code op een slide of aparte link stuurt naar `/login?code=1234&next=/answer/kubecon-2026`
- na login volgt redirect naar `/answer/kubecon-2026`

## Login-redirect gedrag

Het login-scherm moet een `next` parameter ondersteunen.

Voorbeelden:

- `/login?code=1234` → na login naar `/`
- `/login?code=1234&next=/admin` → na login naar `/admin`
- `/login?code=1234&next=/answer/kubecon-2026` → na login naar die questionnaire

Regel:

- `next` moet alleen interne paden accepteren
- geen open redirect naar externe URLs

---

## Backend plan

## Fase 1: globale demo-code invoeren

We halen de afhankelijkheid weg van per-session join codes.

Te implementeren:

- configveld voor één globale rollende code of rotatiestrategie
- validatiefunctie voor die ene code
- geen noodzaak meer om `status.joinCode` in `QuizSession` te patchen

Open keuze:

### Variant A: simpele statische code voor de demo

Bijvoorbeeld via env:

- `DEMO_ACCESS_CODE=1234`

Voordeel:

- extreem simpel
- goed genoeg voor een presentatie

Nadeel:

- niet echt rollend

### Variant B: één globale rollende code in memory

De backend genereert iedere `N` seconden een nieuwe code en bewaart:

- huidige code
- eventueel vorige code voor een korte overlap

Voordeel:

- blijft het “rollende code”-gevoel houden

Nadeel:

- iets meer implementatiewerk

Voorkeur: **Variant B**, maar alleen als de implementatie echt klein blijft. Anders eerst Variant A.

## Fase 2: gedeelde sessiecookie maken

Nieuwe cookie, bijvoorbeeld:

- `participant_session`

Of hergebruik van de bestaande cookie-naam als we het oude model volledig vervangen.

Belangrijk:

- één cookie voor alles
- nickname verplicht
- cookie-expiry lang genoeg voor de demo

## Fase 3: login/session endpoints toevoegen

Nieuwe endpoints:

- `POST /public/login`
- `GET /public/session`
- eventueel `POST /public/logout`

`/public/logout` is niet verplicht, maar kan handig zijn voor demo-reset of wisselen van naam.

## Fase 4: alle relevante endpoints achter dezelfde sessie zetten

Te beschermen:

- `GET /public/storefront`
- `GET /public/storefront/watch`
- `POST /public/orders`
- `GET/PATCH /public/admin/coffeeconfig`
- `GET /public/admin/orders`
- `GET /public/admin/orders/stream`
- `GET /public/admin/session`
- eventueel `GET /public/build-info` publiek laten

Voor questionnaire/Kubernetes:

- `GET /public/session-info`
- browserflows die via forward auth lopen
- questionnaire fetch en submit calls

## Fase 5: audit overal uit sessie halen

De backend moet overal de nickname uit de gedeelde sessie halen.

Niet meer vertrouwen op:

- editable frontend actor fields
- losse request headers voor identiteit

Te gebruiken voor:

- coffee config change log
- order metadata
- eventueel questionnaire submissions

---

## Frontend plan

## 1. Nieuwe centrale login-route

Nieuwe route:

- `/login`

Deze route:

- leest `code` uit de querystring
- leest optioneel `next`
- toont alleen nickname-invoer als de code al uit QR komt
- of toont nickname + code als fallback wanneer iemand direct op `/login` landt

Formulier:

- nickname
- code, verborgen of zichtbaar afhankelijk van context
- knop `Continue`

## 2. Route guard voor protected routes

We willen één simpele guard voor alles behalve:

- `/login`
- eventueel een paar expliciet publieke assets/pages

Protected routes:

- `/`
- `/admin`
- `/admin/orders`
- `/answer/:session`
- eventueel `/thanks`

Flow:

1. gebruiker opent protected route
2. frontend checkt `GET /public/session`
3. bij `401` redirect naar `/login?next=<huidige route>`

## 3. Coffee als hoofdscherm

`/` blijft de koffie-home.

Na login:

- standaard redirect naar `/`
- storefront laadt pas als sessie aanwezig is

## 4. Admin login verwijderen

De bestaande losse admin-loginformulieren in:

- `AdminScreen.vue`
- `AdminOrdersScreen.vue`

verdwijnen.

Die schermen tonen alleen:

- `Signed in as <nickname>`

en gaan er verder van uit dat de router al heeft afgedwongen dat de sessie bestaat.

## 5. Questionnaire-routes terugzetten

De questionnaire-code zit nog grotendeels in de repo.

Terug te brengen:

- `JoinScreen.vue` waarschijnlijk niet meer als hoofdflow gebruiken
- `AnswerScreen.vue` weer routeren
- eventueel `/answer/:session`

Voorkeur:

- `JoinScreen` niet meer gebruiken als gebruikersentree
- login en join samenvoegen in `/login`

De bestaande answer-flow kan dan gewoon laden op basis van routeparam `session`.

## 6. Redirect-na-login ondersteunen

Na succesvolle login:

- als `next` aanwezig is, daarheen
- anders naar `/`

Dat maakt de questionnaire-flow verborgen maar bruikbaar.

---

## Questionnaire-plan

## Huidige observatie

De questionnaire-stukken lijken nog grotendeels aanwezig:

- `AnswerScreen.vue`
- stores voor session en draft submission
- kube API helpers
- question renderer componenten

Wat ontbreekt of niet meer actief lijkt:

- routes in `router.ts`
- centrale entreeflow

## Nieuwe questionnaire-aanpak

De questionnaire wordt route-gebaseerd in plaats van code-gebaseerd.

Voorbeeld:

- `/answer/kubecon-2026`

De login-code geeft alleen algemene toegang.
De route bepaalt welke questionnaire geladen moet worden.

## Voordelen hiervan

- maar één code voor alles
- geen `status.joinCode` meer nodig op `QuizSession`
- minder state in de backend
- minder verwarring tussen auth en inhoud

## Mogelijke beperking

Als er meerdere questionnaires tegelijk live zouden moeten zijn, moet de spreker zelf de juiste redirect/link gebruiken.

Voor de demo is dat acceptabel.

---

## QR-code strategie

## Hoofd-QR voor koffie / algemene toegang

De standaard QR-code wijst naar:

```text
/login?code=1234
```

Na login gaat de gebruiker naar:

```text
/
```

## Verborgen questionnaire-QR

Als we questionnaire willen gebruiken zonder menu-link:

```text
/login?code=1234&next=/answer/kubecon-2026
```

Na login komt de gebruiker direct in de questionnaire.

## Admin-QR optioneel

Als je iemand direct op admin wilt laten landen:

```text
/login?code=1234&next=/admin
```

Dat kan handig zijn voor een demo-slide.

---

## Migratie van het huidige model

## Wat we willen opruimen

- aparte admin-login met wachtwoord
- aparte admin-cookie
- participant-cookie als extra laag naast admin
- per-questionnaire join codes in status
- join-screen als verplicht tussenstation

## Wat we willen behouden

- nickname-gebaseerde audit trail
- bestaande coffee runtime en change log
- bestaande questionnaire-rendering
- bestaande backend session-cookie signing infrastructuur

---

## Concrete implementatiestappen

## Stap 1: docs en decisions vastzetten

Beslissen:

- gebruiken we echt één globale code
- statisch of rollend
- definitieve questionnaire-route

## Stap 2: backend auth vereenvoudigen

Aanpassen:

- één gedeelde sessiecookie
- `POST /public/login`
- `GET /public/session`
- middleware voor gedeelde sessie

## Stap 3: globale code validatie toevoegen

Implementeren:

- code check
- rotatie als gewenst
- oude `status.joinCode` dependency uitschakelen

## Stap 4: admin auth flow verwijderen

Aanpassen:

- admin endpoints achter gedeelde sessie
- admin frontend-login verwijderen
- nickname uit sessie gebruiken

## Stap 5: coffee flow achter login zetten

Aanpassen:

- storefront pas laden na sessie
- route guard naar `/login`
- na login redirect naar `/`

## Stap 6: questionnaire-routes herstellen

Aanpassen:

- `router.ts`
- `AnswerScreen.vue`
- eventueel verwijderen of ombouwen van `JoinScreen.vue`

## Stap 7: QR + next flow afronden

Aanpassen:

- login page ondersteunt `code` en `next`
- redirects netjes afhandelen
- querystring met `code` zo snel mogelijk opschonen na login

## Stap 8: audit en UX afronden

Aanpassen:

- `Signed in as ...` op relevante schermen
- orders voorzien van nickname
- questionnaire submissions waar mogelijk voorzien van nickname

---

## Open keuzes

## 1. Statische of rollende globale code

Voorkeur:

- eerst statisch als we snel iets werkends willen
- daarna eventueel rollend maken

## 2. Logout wel of niet

Voor de demo niet strikt nodig, maar wel handig voor:

- wisselen van nickname
- opnieuw testen

## 3. Questionnaire route naming

Opties:

- `/answer/:session`
- `/quiz/:session`

Voorkeur:

- houden wat al grotendeels bestaat: `/answer/:session`

---

## Aanbevolen implementatievolgorde

1. gedeelde login route en gedeelde cookie
2. admin achter die login
3. coffee achter die login
4. questionnaire routes terugzetten
5. globale code loskoppelen van `QuizSession.status`
6. QR/redirect flows afmaken

---

## Samenvatting

De simpelste versie van dit plan is:

- één QR-code
- één globale code
- één extra stap om nickname in te vullen
- daarna één gedeelde sessie voor koffie, admin en questionnaire
- questionnaire alleen via directe redirect-link, niet via menu
- koffie op `/`
- iedereen krijgt admin-rechten
- alle acties worden gelabeld met nickname

Dat houdt de demo snel, herkenbaar en goed observeerbaar.
