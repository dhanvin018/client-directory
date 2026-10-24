# Desktop clients

The editor is a desktop application. The directory was first built around a
browser client; this page explains how a desktop client talks to it, what
changed to support that, and how web and desktop communication differ.

Both kinds of client are supported side by side: nothing a browser client
relies on was removed.

## Web vs desktop communication

| Concern | Browser client | Desktop client |
|---|---|---|
| **Who makes the HTTP requests** | Page JavaScript, inside the browser's security model | The app's own process (for Wails, the Go side), like any program |
| **Origin and CORS** | Every request carries an `Origin`; the server must allow it (`server.allowedOrigins`) or the browser blocks the response | No `Origin`, no CORS. CORS only applies if the app's webview JavaScript calls the API directly (see below) |
| **Session storage** | `HttpOnly` cookie the browser stores and sends automatically. Page scripts cannot read it (protects against XSS) | The app holds the token itself and stores it in the OS keychain (Windows Credential Manager, macOS Keychain, Linux Secret Service) |
| **Sending the session** | Cookie, attached by the browser to every request | `Authorization: Bearer <token>`, attached by the app |
| **CSRF** | A real risk with cookies: another site can make the browser send them. Mitigated by `SameSite=Lax` and explicit CORS origins | Does not apply: nothing sends the token automatically |
| **Finishing OAuth sign-in** | The provider redirects the browser back to the directory, which sets the cookie and redirects to the web app | The sign-in happens in the **system browser**, which cannot hand a cookie to the app. The directory delivers a one-time code to a listener the app runs on `127.0.0.1`, and the app exchanges it for a token |
| **Keeping secrets** | The web server is a confidential client: Google's client secret stays on the server | An installed app is a **public client**: anything shipped in it can be extracted, so it must hold no secret. PKCE proves the app that finishes sign-in is the one that started it |
| **Links into the app** | Plain `https://` URLs open the page | A custom URL scheme (`collabdoc://…`) registered by the installer opens the app; a web link is still needed for people who do not have it |
| **Notifications** | Only while a tab is open | Background process and OS notifications are possible (via the collaboration server's WebSocket, future) |
| **Network identity** | Requests pass through the browser (proxies, extensions) | Direct; the app can pin TLS settings, retry and work offline |

## Desktop sign-in

The flow follows **RFC 8252** (OAuth 2.0 for native apps) with **PKCE**
(RFC 7636). The directory stays the only OAuth client Google knows, so the
Google client secret never leaves the server.

```mermaid
sequenceDiagram
    participant A as Desktop app
    participant L as App's loopback listener<br/>(127.0.0.1:port)
    participant B as System browser
    participant D as Directory
    participant G as Google
    A->>A: verifier = 43-128 random chars<br/>challenge = base64url(SHA-256(verifier))<br/>appState = random
    A->>L: listen on 127.0.0.1:<random port>
    A->>B: open /auth/google/login?redirect_uri=http://127.0.0.1:port/callback<br/>&code_challenge=…&code_challenge_method=S256&state=appState
    B->>D: GET /auth/google/login?…
    D->>D: validate (loopback only, S256), store pending login (10 min)
    D-->>B: 302 to Google (+ oauth_state cookie)
    B->>G: user signs in
    G-->>B: 302 /auth/google/callback?code&state
    B->>D: GET /auth/google/callback
    D->>G: exchange code, read identity
    D->>D: find or create peer; issue one-time code (60 s, single use)
    D-->>B: 302 http://127.0.0.1:port/callback?code=…&state=appState
    B->>L: GET /callback?code&state
    L-->>B: "You can close this tab"
    L->>A: code, state
    A->>A: state == appState? (else discard)
    A->>D: POST /auth/token {code, codeVerifier, redirectUri}
    D->>D: SHA-256(verifier) == stored challenge? code unused and fresh?
    D-->>A: 200 {accessToken, expiresAt, refreshToken, refreshExpiresAt, peer}
    A->>A: refresh token → OS keychain; access token in memory
    A->>D: API calls with Authorization: Bearer …
```

### What protects each step

| Threat | Protection |
|---|---|
| Another program on the machine grabs the code from the loopback redirect | PKCE: the code is useless without the verifier, which never leaves the app |
| A forged response reaches the app's listener | The app checks its own `state` and discards anything else |
| The redirect is pointed at an attacker's server (open redirect) | `redirect_uri` must be `http://127.0.0.1:<port>` or `http://[::1]:<port>`; anything else is rejected with 400 before the user is sent to Google. `localhost` is rejected because it can resolve elsewhere |
| A code is used twice, or guessed | Single use (deleted on redemption, even a failed one), 60 s lifetime, 256 random bits, only its SHA-256 is stored |
| Login CSRF in the browser | The existing `oauth_state` cookie check, unchanged |

On failure the browser is sent to the loopback URI with `error` instead of
`code` (`access_denied`, `invalid_request`, `server_error`) and the app's
state, so the app can show what happened.

### What the desktop app has to do

1. Generate the verifier (43–128 characters from `A-Z a-z 0-9 - . _ ~`), the
   S256 challenge, and a random state.
2. Start an HTTP listener on `127.0.0.1` with port 0 (the OS picks a free
   port), path e.g. `/callback`.
3. Open the system browser (Wails: `runtime.BrowserOpenURL`) at
   `/auth/google/login` with the four query parameters.
4. When the listener receives `code` and the matching `state`, answer with a
   short "you can close this tab" page, stop the listener, and `POST
   /auth/token`.
5. Store `refreshToken` in the OS keychain (for Go, a library such as
   `github.com/zalando/go-keyring`). Keep `accessToken` in memory and send it
   as a bearer token.
6. Before `expiresAt`, or on a 401, call `POST /auth/refresh` with
   `{"refreshToken": …}` and replace **both** tokens (the old refresh token is
   spent). Run one refresh at a time: two concurrent refreshes with the same
   token look like theft and end the session. If refresh returns 401, delete
   the keychain entry and start sign-in again.
7. On start-up, read the refresh token from the keychain and refresh; the user
   only goes through the browser again after `auth.sessionDuration` (30 days)
   without using the app, or after logging out.
8. Logout: `POST /auth/logout` with `{"refreshToken": …}`, then delete the
   keychain entry. "Sign out everywhere" is `POST /auth/logout-all` with the
   bearer token.

Do not use an embedded webview for the Google sign-in: Google blocks OAuth in
embedded webviews, and the system browser lets users reuse their existing
Google session and password manager.

## Invite links

Invite responses now carry two links:

| Field | Example | For |
|---|---|---|
| `url` | `http://localhost:3000/invite/{code}` | Sharing anywhere (chat, email). Should be a small web page that opens the app, or explains how to install it |
| `appUrl` | `collabdoc://invite/{code}` | Opening the desktop app directly. Present when `invites.appLinkBaseUrl` is set |

The installer must register the `collabdoc` scheme with the OS (Wails
supports this per platform). When the app is opened with such a URL it calls
`GET /api/v1/invites/{code}` and then accept or decline, exactly like the web
flow. The scheme name is a placeholder: choose the final one before the first
release, since changing it breaks links people have already shared.

## Calling the API from a Wails app

Make API calls from the **Go side** of the app and expose results to the UI
through Wails bindings. Then there is no CORS, the token never enters page
JavaScript (where an XSS bug could read it), and the same code works for
WebSockets later.

If the webview JavaScript must call the API directly, add the webview's origin
to `server.allowedOrigins` (it differs by platform and Wails version; check the
`Origin` header the app actually sends) and send the token as a bearer header;
cookies are not used by desktop clients.

## What changed in the directory

| Area | Change |
|---|---|
| `GET /auth/{provider}/login` | Accepts `redirect_uri`, `code_challenge`, `code_challenge_method=S256`, `state` for desktop sign-in. Without them it behaves exactly as before |
| `GET /auth/{provider}/callback` | For a desktop sign-in, redirects to the app's loopback URI with a one-time code (or an error) instead of setting the cookie |
| `POST /auth/token` | New, public. Code + verifier + redirect URI in, bearer token and peer out (`Cache-Control: no-store`) |
| `service.DesktopLoginService` | New. Validation of loopback URIs and PKCE, pending logins, one-time codes |
| Migration `0011_desktop_login.sql` | New tables `desktop_logins` and `desktop_auth_codes` (short-lived, single-use rows; stored in Postgres so they work across restarts and multiple instances) |
| Invites | Responses add `appUrl`; config `invites.appLinkBaseUrl` (env `INVITE_APP_LINK_BASE_URL`, default `collabdoc://invite/`) |
| Sessions (migration `0012_sessions.sql`) | Sign-in returns a 15-minute access token and a 30-day rotating refresh token; `POST /auth/refresh`, `POST /auth/logout-all`; logout is stored in Postgres. Applies to browsers too (cookies `session` and `refresh`). See [api.md](api.md#sessions-refresh-and-logout) |
| Unchanged | Bearer authentication on every `/api/v1` route (already supported), browser sign-in, CORS |
