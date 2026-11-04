# Plan: Voter can take its menu and keys from a folder

_Written 2026-11-04. This is the Voter side of
[demo-production-settings-image-plan.md](demo-production-settings-image-plan.md): that document says why and
covers the cluster, this one says how Voter is built._

## What Voter gets

Three independent switches, all off by default. The current way of deploying keeps working, and stays a
supported path rather than a leftover.

| Switch | Values | What it does |
| --- | --- | --- |
| `SHOP_ONLY` | on / off. **Exists already, unchanged** | Read-only: the page is the coffee bar and offers no editing |
| `SETTINGS_DIR` | a path | Read the menu from this folder at startup, and build no Kubernetes client |
| `SETTINGS_KEYS` | empty, `openbao-age` | How `sops` gets its key. Empty opens nothing |
| `DONT_DO_THIS` | on / off | Registers the `/dont-do-this` page |

| Deployment | `SHOP_ONLY` | `SETTINGS_DIR` | `SETTINGS_KEYS` | `DONT_DO_THIS` |
| --- | --- | --- | --- | --- |
| demo, as today | off | | | |
| production, as today: menu from Kubernetes, live stream | **on** | | | |
| Menu from a folder, no keys | on | set | | |
| production after the cutover | on | `/settings/resources` | `openbao-age` | on |
| Local run, no cluster | on | any folder with a menu | | |
| Later, with a real key | on | `/settings/resources` | `openbao-transit` | **off** |

Going back to today's production is removing the `SETTINGS_*` lines. The binary keeps both paths.

**Voter refuses to start on a combination that cannot work**, from a `validate()` called by `loadConfig`, with a
table test:

| Combination | Why |
| --- | --- |
| `SETTINGS_DIR` without `SHOP_ONLY` | The editor writes a CoffeeConfig through `/k8s` while orders would read the folder, so the screen and the till disagree. Allowing it later is removing this line |
| `SETTINGS_KEYS` without `SETTINGS_DIR` | There is no folder to open anything from |
| `DONT_DO_THIS` without `SETTINGS_KEYS` | The page would have nothing to show |
| `openbao-age` without `OPENBAO_ADDR`, `OPENBAO_ROLE`, `OPENBAO_AGE_SECRET_PATH`, `OPENBAO_TOKEN_FILE` | It cannot log in |
| An unknown `SETTINGS_KEYS` | A typo must not read as "no keys" |

## Backend

Everything stays flat in `package main`, like the rest of `voter/`.

### The folder knows nothing about images

`SETTINGS_DIR` is the directory that holds the files, so `/settings/resources` rather than `/settings`. Voter reads
`*.yaml` in that directory, not below it. The same code then works on an image volume, a ConfigMap or a local folder.

It decides what a file is from its content, never its name, because a recreated Secret comes back as
`voter-<name>.sops.yaml`:

| The file is | Voter |
| --- | --- |
| `kind: CoffeeConfig`, `metadata.name` = `COFFEE_CONFIG_NAME` | Takes it as the menu. **Missing or invalid: exit non-zero** |
| `kind: Secret` with a `sops:` block | Queues it for opening, under its `metadata.name` |
| `kind: Secret` without `sops:` | Logs the name and skips it. Plaintext is never held |
| Anything else, or not YAML | Logs the name and skips it |

Strict about the menu, relaxed about the rest: a stray file must not take down the coffee bar. Invalid means it does
not parse, has no currency, or has no products. The CRD's other rules are not repeated here.

### `settings.go` (new)

```go
// What this pod loaded from SETTINGS_DIR. Set once before serving and never
// changed, so readers need no lock.
type settings struct {
	menu      coffeeConfig
	revision  string // sha256 of the menu file's bytes, cut to 16 hex: what the browser compares
	reference string // SETTINGS_REFERENCE, for people to read. Nothing depends on it
	keys      *settingsKeys // nil when SETTINGS_KEYS is empty
}

func loadSettings(cfg config) (*settings, error)
```

`sigs.k8s.io/yaml` moves from indirect to direct in `go.mod`. It turns YAML into JSON, and `coffeeConfig` already has
JSON tags.

### Where it joins the existing code

`main.go` builds one or the other. The reconcilers need no change: they already sit behind `!cfg.ShopOnly`, and
`SETTINGS_DIR` requires `SHOP_ONLY`.

```go
if cfg.SettingsDir != "" {
	loaded, err = loadSettings(cfg)
} else {
	serviceAccount, err = newServiceAccountClient(&cfg)
}
```

`readCoffeeConfig` is the one place that reads the menu, so it gets the one branch:

```go
func (d handlerDeps) readCoffeeConfig(ctx context.Context) (coffeeConfig, error) {
	if d.settings != nil {
		return d.settings.menu, nil
	}
	// As today.
```

This is deliberately not a `menuSource` interface: two sources and one caller don't need one, and the fake-client
tests stay as they are. `buildStorefront` and `prepareCoffeeOrder` only read the config, so the shared menu is safe.

### `settings_keys.go` (new)

The flexibility in decrypting is one function type and one `switch`:

```go
// The environment for a sops child, and all of it: the child inherits nothing.
type sopsCredentials func(ctx context.Context) (env []string, err error)

func newSopsCredentials(cfg config) (sopsCredentials, error) {
	switch cfg.SettingsKeys {
	case "":
		return nil, nil
	case "openbao-age": // log in, read the age key, return SOPS_AGE_KEY=...
		return newOpenBaoAge(cfg), nil
	}
	return nil, fmt.Errorf("unknown SETTINGS_KEYS %q", cfg.SettingsKeys)
}
```

- **A key is never read from Voter's own environment or a mounted file.** There is no such mode, and setting
  `SOPS_AGE_KEY` on the pod does nothing, because the child inherits nothing.
- **`openbao-transit` later** is one more `case` returning `VAULT_ADDR` and `VAULT_TOKEN`. `sops` does the rest.
- **Opening a file** is `exec.CommandContext(ctx, "sops", "-d", path)` with `cmd.Env` set to exactly that list and a
  30-second timeout, one file at a time. Stdout is parsed as a Secret and `data:` is base64-decoded. Stdout is never
  logged. Stderr goes in the reason, cut to one line.
- **It runs in the background**, so it never holds up readiness. It retries what has not opened with backoff, from
  5 seconds up to 5 minutes, and logs in again each round. It stops once everything is open. Whatever is open stays
  open.
- **Any encrypted Secret in the folder opens**, with no list of names, so a third key needs no Go change.
- **Nothing in Voter uses a key yet.** `/dont-do-this` is the only reader, so the opener stays small on purpose.

Values get a type that cannot leak by accident, so only one kind of call can expose a value, and it is easy to grep:

```go
type secretValue string

func (secretValue) String() string   { return "[redacted]" } // %s and %v
func (secretValue) GoString() string { return "[redacted]" } // %#v
func (secretValue) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (v secretValue) reveal() string { return string(v) }    // dont_do_this.go, nothing else
```

`Dockerfile` gets the same `sops` coffee-settings pins. It is static and 52 MB, and unused unless `SETTINGS_KEYS` is set:

```dockerfile
COPY --from=ghcr.io/getsops/sops:v3.13.3-alpine@sha256:ae501277bf742f1662e0f881f43dd8fd6798b489a8058e921dbf6cda597140ea /usr/local/bin/sops /usr/local/bin/sops
```

Voter owns none of the SOPS format. `sops` does the MAC, the data key and the refusals.

### How it fails

- **A bad menu: exit non-zero.** With `maxUnavailable: 0` the old pod keeps serving.
- **OpenBao down, or a key that will not open: keep selling.** Log why, retry, and show it on `/dont-do-this`.
- **No file watching.** An image volume never changes under a pod; a new image is a new pod.

### `/dont-do-this` (`dont_do_this.go`, new)

A small page rendered by Voter, not part of the Vue app. With the switch off it answers 404, rather than falling
through to the SPA.

```text
Don't do this.
Normally this page would not exist. A workload opens a key, uses it, and never shows it to anybody.
These values are made up, and they are already public on coffee-settings.k8s.koudijs.dev/decrypted/.

  coffee-mail      apiKey   made-up-value
    on disk        /settings/resources/coffee-mail.sops.yaml      ENC[AES256_GCM,data:gxfeGP4q…
  coffee-payments  apiKey   other-made-up-value
    on disk        /settings/resources/coffee-payments.sops.yaml  ENC[AES256_GCM,data:…

Where these values are, checked just now by this process
  Disk               the mounted image holds ciphertext only (read just now, above)
  Environment        not in this process's environment
  Kubernetes         no ServiceAccount token in this pod, so it could not read a Secret anyway
  This process       here, and only here. Gone when the pod goes
  The age key        fetched from OpenBao, handed to sops, not kept

Opened 14:02:11 from cr.k8s.koudijs.dev/configbutler/coffee-settings:main-…@sha256:…
```

- The three "checked" lines are real checks at render time: the encrypted files are read again, the environment is
  scanned for the values, and the token path is checked to be absent. The other two state how the code works.
- **Public**, on the host's default route, like `/decrypted/`. The same values from the same image are already public
  there, so a login would protect nothing.
- **Rendered with `html/template`, with `no-store` and `X-Robots-Tag: noindex`.** The values are whatever somebody
  typed on `/room`, so they must be escaped: this page shares an origin with the login session.
- A key that did not open shows as `unavailable`, with the reason.
- **Before a real key ever goes into this image, `DONT_DO_THIS` comes out of the Deployment.**

### `http_handlers.go` and the storefront

| Change | Why |
| --- | --- |
| `GET /revision.json` → `{"revision":"…"}`, `no-store`, always registered. `""` when the menu is from Kubernetes | The browser's poll, next section |
| `/config.json` gains `menuSource: "kubernetes" \| "folder"` | Names what it is, rather than a `settingsImage` flag |
| `storefrontResponse` gains `Revision string` with `json:"revision,omitempty"`, set in the handler after `buildStorefront` | The answer carries the revision of the pod that gave it. `buildStorefront` stays pure |

`/revision.json` sits beside `/config.json` and **not under `/public/`**. Every `/public/` request goes through
krm-foyer's ForwardAuth. A poll per phone per second must not, and this menu revision is no secret. The host's default
route already sends it to Voter, with no cluster change.

It hashes the menu file only, not the encrypted files:

- A new key changes nothing a phone shows, so it should not make phones do anything.
- It keeps the rule from [coffee-api-keys-design.md](coffee-api-keys-design.md): nothing derived from a key is shown.

### Stopping cleanly (needed for the rolling update, good for every mode)

Voter has no signal handling today, so SIGTERM ends it at once, mid-request. Under `Recreate` nobody could tell. Under
a rolling update, an order in flight on the old pod would turn into a 502, right when the new pod is fine.

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
defer stop()
done := make(chan struct{})
go func() {
	defer close(done)
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}()
if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
	log.Fatal(err)
}
<-done
```

The wait for the edge to notice belongs in the Deployment, not in Voter: `lifecycle: {preStop: {sleep: {seconds: 5}}}`.
The pod leaves the Service at once and keeps serving for 5 seconds while Traefik catches up, and only then gets
SIGTERM. So demo, which has no `preStop`, simply stops faster and cleaner. The 20-second timeout fits inside the
default 30-second grace period. The comment in `main.go` that says this binary has no graceful shutdown needs updating;
the reconcilers can still just die with the process.

## The browser: noticing a new pod

**Your instinct is right: a small call every second.** The rolling update is what makes it subtle, and there is one
real trap. Two requests from one phone can reach different pods, so "the poll says new" does not mean "the refetch
got new".

```text
                  old pod (menu A)      new pod (menu B)     a phone's polls
merge             serving               pulling the image    A A A A
new pod Ready     serving               serving              A B B A      both are in the Service
old pod deleted   preStop, 5 s          serving              B B          out of the Service, still answering
then              SIGTERM, drains       serving              B B B  ->  refetch, stamped B
```

Three rules make that safe.

1. **Ask for the content's revision, not the pod's identity.** A restart with the same menu changes nothing, so a node
   drain makes no phone do anything. The hash is compared with `!=`, not `>`, so a rollback is just another difference.
   That also holds for more than one replica.
2. **Compare the poll with the stamp on what is on screen, every time.** `/public/storefront` says which revision it
   is. If the refetch lands on the old pod, the stamp still says A, the poll still says B, and the phone tries again.
   This is why it cannot get stuck: it acts on a state, not on an event it could miss. Remembering "I saw B" and
   refetching once would leave a phone on the old menu for good.
3. **Act on three identical answers in a row.** That sits out the overlap, when answers alternate. The cost is about
   3 seconds of delay. A phone that gets lucky refetches early, and the worst outcome is a second or two on the wrong
   menu before rule 2 corrects it.

### `frontend/src/api/menuRevision.ts` (new)

```ts
const POLL_MS = 1000
const AGREE = 3

export function useMenuRevision(shown: () => string | undefined, refresh: () => Promise<void>) {
  let last = '', agreed = 0, timer = 0, stopped = false

  async function tick() {
    if (!document.hidden) {
      const seen = await readRevision() // undefined on any failure, including HTML with a 200
      if (seen !== undefined) {
        agreed = seen === last ? agreed + 1 : 1
        last = seen
        if (agreed >= AGREE && seen !== shown()) {
          agreed = 0 // a failing refetch retries every 3 seconds, not every second
          await refresh()
        }
      }
    }
    if (!stopped) timer = window.setTimeout(tick, POLL_MS)
  }

  onMounted(tick)
  onUnmounted(() => { stopped = true; clearTimeout(timer) })
}
```

- `setTimeout` after each answer, not `setInterval`, so slow answers never pile up. `readRevision` uses a 2-second
  `AbortSignal.timeout`.
- **A pod older than this feature answers `/revision.json` with `index.html` and a 200**, through the SPA fallback in
  `static.go`. `readRevision` must treat a non-JSON body as no answer.
- It runs on the order screen only, and a hidden tab polls nothing. A phone coming out of a pocket catches up within
  about 3 seconds.

### **Refetch, don't reload**

`refreshStorefront()` already exists in `OrderScreen.vue`. It keeps the cart and scroll position, with no flash on a
phone. A reload isn't needed to stay working either: `router.ts` imports every screen eagerly, so there is no lazy
chunk to go missing when the old pod leaves.

### The screens

| File | Change |
| --- | --- |
| `api/appConfig.ts` | `menuSource` |
| `shopOnly.ts` | `menuFromFolder()` beside `shopOnly()` |
| `coffeeTypes.ts` | `StorefrontResponse.revision?: string` |
| `OrderScreen.vue` | With a folder, `useMenuRevision(() => cart.storefront?.revision, refreshStorefront)` instead of `useLiveCoffeeConfig`. The panel becomes `v-if="live && !live.synced.value"`. `invalidItems` reads `storefront.products` in this mode, because `cart.activeConfig` is only filled by the stream |
| `IdentityScreen.vue` | "What you may do" moves into its own component, which is not rendered with a folder, so `useAuthorization` never starts and nothing calls `/k8s`. Session and `/auth/whoami` stay |

"Prices update live" stays in the copy, because it is still true.

### What a phone does in each case

| Case | Polls see | The phone |
| --- | --- | --- |
| New menu | B | Updates its menu within about 3 seconds of the old pod leaving, and keeps its cart |
| Rollback | The older menu's hash | The same |
| Only a key changed | The same revision | Nothing. The pod swap is invisible |
| Restart with no change | The same revision | Nothing |
| New pod crashes on a bad menu | A | Nothing. The old pod keeps serving and the rollout stalls |
| A poll fails: 502, network, timeout | No answer | Keeps its menu, and the count does not move |
| An item in the cart left the menu | B | "Cart needs attention", the existing panel, and ordering is blocked until it is out of the cart |
| An order in flight at the switch | | Finishes on the pod that took it (preStop, then `Shutdown`). The server prices it, and the thank-you page shows the server's answer |
| A tab left open across the cutover itself | | Stays in the mode it loaded in, until it reloads. Its stream breaks when the routes go, and the reconnecting panel shows. Once only |

### Why not something cleverer

An SSE stream or long poll would save about 2 seconds, but it holds a connection per phone, needs `Shutdown` to cut it,
and needs reconnect logic. Polling costs one small answer per visible phone per second, with no API call and no
krm-foyer in the path: 200 phones are 200 requests a second of about 30 bytes. Reconsider it if that ever shows up on a
graph.

## Left out on purpose

- **Reloading without a restart** (file watching, SIGHUP): an image volume never changes under a pod.
- **An encrypted menu:** the menu is public.
- **Refusing an order placed against an old revision:** the server prices it and says so.
- **Reloading the page on a new Voter build:** same as today, and not about config.
- **More than one replica:** orders and voucher counts are in memory. The revision design would cope; those would not.
- **A browser spec for this mode:** the Vitest cases below cover the logic.

## Tests

Tests that need `sops` skip when it is not installed. They generate a throwaway age key and encrypt their files in a
temporary folder, so no key and no encrypted fixture is committed. OpenBao is a small fake HTTP server.

| Where | Case |
| --- | --- |
| Go | The `validate()` table. `env` is refused like any other unknown `SETTINGS_KEYS` |
| Go | The menu loads and a `voter-coffee-mail.sops.yaml` is still found. A bad menu refuses to start. No client is built |
| Go | `/public/` still refuses a request without the identity header |
| Go | A key that will not open still sells, and is retried. A `SOPS_AGE_KEY` in Voter's own environment does not reach the child |
| Go | `/dont-do-this` is 404 by default and escapes `<script>`. A key's value appears in no other response and no log |
| Go | With no switches set, the storefront still reads through the fake client and `/config.json` says `kubernetes` |
| Go | `/revision.json`: `no-store`, the same hash for the same menu, a new hash for a new menu, and unchanged when only an encrypted file changes |
| Go | `/public/storefront` is stamped in folder mode and has no `revision` otherwise |
| Go | A folder mounted the ConfigMap way (symlinks, a `..data` directory) loads |
| Go | `secretValue` prints `[redacted]` through `%s`, `%v`, `%#v` and `json.Marshal` |
| Go | A request still in flight when `Shutdown` starts gets its answer |
| Vitest | A B A B does not refresh, and B B B does |
| Vitest | A refresh that returns the old stamp is tried again, and stops once the stamp matches |
| Vitest | A rollback (B back to A) refreshes |
| Vitest | HTML with a 200, a 502 and a network error all leave the count and the menu alone |
| Vitest | A hidden page polls nothing, and unmounting stops the timer |
| Vitest | With a folder, a cart item on the menu is not invalid and one off it is |

Running it locally needs no cluster: `SHOP_ONLY=true SETTINGS_DIR=<folder> COFFEE_CONFIG_NAME=demo-coffee go run .`
Opening keys needs an OpenBao to log in to, which is what the fake server is for in the tests.

## Order of work

Each step works and ships on its own, off by default, in one PR. **Merge not before Friday 6 November 11:55**: a release
restarts Voter in both namespaces, even with every switch off.

| # | Step | Result |
| --- | --- | --- |
| 1 | SIGTERM handling | Better in every mode |
| 2 | Config, `validate()`, `settings.go`, the branches in `main.go` and `readCoffeeConfig` | Sells from a folder |
| 3 | `/revision.json`, `menuSource`, the stamp, the frontend | Phones follow a new menu |
| 4 | `sops` in the image, `settings_keys.go`, `secretValue`, OpenBao login | Keys open with `openbao-age` |
| 5 | `dont_do_this.go` | The page exists when asked for |

**Built on branch `feat/settings-folder`.** Step 3 was proven the way this plan suggested: two Voter processes behind
something that alternates, with a browser watching it settle (see "What was checked").

## Done when

On the live cluster, beside the "Done when" in the parent plan:

- [ ] With an order screen open and a cart filled, merging a promotion moves the menu on the phone with no touch, and the cart is still there.
- [ ] Reverting moves it back the same way.
- [ ] Polling `/revision.json` and placing orders through the switch shows no 502. Note how long both pods were in the Service.
- [ ] A promotion that changes only a key makes no phone refetch.
- [ ] demo and any shop-only deployment without `SETTINGS_DIR` behave as before, with the stream live.

## Risks taken on purpose

| Risk | What happens | Why that is fine for now |
| --- | --- | --- |
| **Two ledgers during the overlap** | For that moment, an order can land on either pod, each with its own voucher count and feed. The old pod's are then lost | The parent plan already accepts losing them at every merge |
| **"Forgotten" is not "wiped"** | Go cannot clear a string. The age key stays in memory until the collector reuses it, and the opened values stay on purpose | Anyone who can read this process's memory can already read the values, and can fetch the age key with its token |
| **A phone may flicker back** | One lucky run of three answers during the overlap can refetch the wrong way for a second or two | Rule 2 corrects it, and this is a menu |

## What was checked for this plan

| Fact | How |
| --- | --- |
| The image holds `resources/coffeeconfig.yaml`, `coffee-mail.sops.yaml` and `coffee-payments.sops.yaml`. No `raw/` yet | Live listing, `coffee-settings.k8s.koudijs.dev/resources/` |
| The image's menu is named `demo-coffee`, as production's `COFFEE_CONFIG_NAME` is | Live file, `production/app.yaml` |
| `metadata.name` is readable in an encrypted file, so a Secret can be found without opening it | Live file |
| Kubernetes on the cluster is v1.37.0 and `preStop.sleep` exists | `kubectl version`, `kubectl explain` |
| Production runs `Recreate`, a 30-second grace period and no `minReadySeconds` | `kubectl get deploy` |
| Voter has no signal handling or `Shutdown` | `grep` |
| The default route has no ForwardAuth, and `/public/` does | `production/ingress.yaml` |
| An unknown path gets `index.html` with a 200 | `static.go` |
| Every screen is imported eagerly | `router.ts` |
| With two real Voter processes behind an alternating proxy and Chromium on the order screen: 5 seconds of alternating answers move nothing. Once the old pod leaves, the phone is on the new menu in about 1.6 seconds, with one refetch, no reload and its cart. A rollback, a refetch that keeps reaching the old pod, a key-only change, HTML with a 200 and 502s all behave as the table above says. SIGTERM exits 0. `/me` makes no `/k8s` call | Throwaway harness, real `sops`, a fake OpenBao |
| `/me` could not open on any shop-only deployment: `SHOP_ROUTES` said `me`, and the route is named `identity`. Fixed here | The harness above |
| In the built image, as uid 65532 on a read-only root with only `SOPS_AGE_KEY` set, `sops` opens a Secret. Voter sells from a folder with no cluster, and `docker stop` exits 0 | `docker run --read-only` |

**Not checked, all of it only possible on the cluster:** the first real run against OpenBao and the mounted image. How long both pods are really in the Service (it should be short, because the Deployment controller
scales the old one down once the new one is available, and "Done when" measures it). How Traefik v3.7.13 treats a
terminating endpoint; the design tolerates either. The load of the poll.
