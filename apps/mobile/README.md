# AeroLab mobile app (Expo / React Native)

**Status: logic tested in Node; UI typechecked; NEVER run on a phone or emulator.** No device, emulator or EAS
build was available. Treat every screen as untested until it has run on Android and iOS.

## What is tested (`pnpm test`, 33 unit tests + 1 opt-in end-to-end)
* **Pairing QR is untrusted input**: strict parser (version, site-id charset, 32-byte key, code format, URL
  hints). `javascript:`, `file:`, public `http://`, URLs with credentials/query/fragment are refused.
* **Site identity**: the phone sends a random challenge; the Pi's Ed25519 signature must verify against the key
  **pinned at pairing** (same site id, same key, our challenge). Verified against a vector produced by the **Go**
  implementation (`contracts/examples/identity.vector.json`, also checked by a Go test) and, opt-in, against a
  **real running edge** over HTTP. An impostor with the right site id but a different key is refused, and the
  credentials are never sent to it.
* **Connection Manager decision table** (spec 7.4): Automatic = verified local + local session → LOCAL, else cloud
  session + reachable → CLOUD, else explicit read-only CACHE with its age, else unavailable. *Local only* never
  touches the cloud and *Cloud only* never touches the LAN. Only a **live** decision permits writes.
* **Source switching**: separate token per (site, source); cache namespaced by site+source+user; a switch aborts
  in-flight requests and discards late responses; relabelled cache records are dropped; a 401 clears only that
  source's token; `post()` refuses unless live.

## What is written but unverified on a device
`src/native.ts` (SecureStore keychain adapter, AsyncStorage adapter, expo-crypto RNG), all screens
(`src/ui`), camera QR scanning (`expo-camera`), `App.tsx`. Not built/verified: push notifications (none — no push
service is configured, and none is promised), live SSE (the app polls every 10 s; RN streaming fetch not relied on),
crop records/harvest (no backend yet), local TLS trust (the app accepts `https://` anywhere and `http://` only on
private/lab addresses, flagged "unencrypted lab link"; a local CA/trust story is still open), Android cleartext /
iOS local-network permission configuration in `app.json`.

## Run
```bash
cd apps/mobile && pnpm install && pnpm typecheck && pnpm test
pnpm start        # Expo dev server; scan with Expo Go (SDK 58) on a phone on the site Wi-Fi
# end-to-end against a real Pi/edge:
AEROLAB_E2E_EDGE=http://<edge> AEROLAB_E2E_PW=<admin password> pnpm test src/e2e
```
Dependency note: `expo install` could not reach Expo's version API in this environment, so the SDK-58 native module
versions were pinned from npm dist-tags. Run `npx expo install --check` on a normal network.
