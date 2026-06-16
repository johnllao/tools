# http-tail — Reference Documentation

> **Directory**: `web/http-tail/` (React + Vite single-page application)
>
> **Purpose**: Browser-based log viewer that connects to the `httptail` Go service
> via Server-Sent Events (SSE), displaying streaming log lines with auto-scroll,
> pause/resume, and clear controls. Uses a dark theme consistent with the
> `web/console/` project.

---

## Table of Contents

1. [Overview & Architecture](#1-overview--architecture)
2. [Project Files](#2-project-files)
3. [Build & Tooling Configuration](#3-build--tooling-configuration)
   - [package.json](#31-packagejson)
   - [vite.config.js](#32-viteconfigjs)
   - [eslint.config.js](#33-eslintconfigjs)
   - [index.html](#34-indexhtml)
   - [.gitignore](#35-gitignore)
4. [Source: main.jsx](#4-source-mainjsx)
   - [Imports](#41-imports)
   - [Constants](#42-constants)
   - [Component: App](#43-component-app)
     - [State Variables](#431-state-variables)
     - [Refs](#432-refs)
     - [Effect: Pause Ref Sync](#433-effect-pause-ref-sync)
     - [Effect: SSE Connection](#434-effect-sse-connection)
     - [Effect: Auto-Scroll](#435-effect-auto-scroll)
     - [Handlers](#436-handlers)
     - [Status Display Map](#437-status-display-map)
     - [Render Tree](#438-render-tree)
   - [Entry Point](#44-entry-point)
5. [Source: styles.css](#5-source-stylescss)
   - [Font Declarations](#51-font-face-declarations)
   - [CSS Custom Properties](#52-css-custom-properties)
   - [Global Reset & Base Styles](#53-global-reset--base-styles)
   - [Scrollbar Styling](#54-scrollbar-styling)
   - [Layout](#55-layout)
   - [Header Bar](#56-header-bar)
   - [Status Indicator](#57-status-indicator)
   - [Buttons](#58-buttons)
   - [Log Display Area](#59-log-display-area)
   - [Empty State](#510-empty-state)
6. [Static Assets](#6-static-assets)
7. [SSE Protocol Integration](#7-sse-protocol-integration)
8. [State Machine](#8-state-machine)
9. [Edge Cases Handled](#9-edge-cases-handled)
10. [Extension / Enhancement Points](#10-extension--enhancement-points)

---

## 1. Overview & Architecture

```
┌──────────────────────────────────────────────────────────┐
│  Browser                                                 │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │  index.html                                        │  │
│  │  <div id="root"> ── mounted by createRoot()        │  │
│  │                                                    │  │
│  │  ┌──────────────────────────────────────────────┐  │  │
│  │  │  <App />                                     │  │  │
│  │  │                                              │  │  │
│  │  │  ┌─────────────────────────────────────────┐ │  │  │
│  │  │  │  .header                                │ │  │  │
│  │  │  │  ┌──────────────┐  ┌──────────────────┐ │ │  │  │
│  │  │  │  │ .header-left │  │ .header-right    │ │ │  │  │
│  │  │  │  │ > tail       │  │ ● Connected (3)  │ │ │  │  │
│  │  │  │  │ /var/log/... │  │ [⏸ Pause] [✕ Clr]│ │ │  │  │
│  │  │  │  └──────────────┘  └──────────────────┘ │ │  │  │
│  │  │  └─────────────────────────────────────────┘ │  │  │
│  │  │                                              │  │  │
│  │  │  ┌─────────────────────────────────────────┐ │  │  │
│  │  │  │  .log-area (scrollable)                 │ │  │  │
│  │  │  │  ┌───────────────────────────────────── │ │  │  │
│  │  │  │  │  .log-line                           │  │  │  │
│  │  │  │  │  .log-line                          │  │  │   │  │
│  │  │  │  │  ... (streaming from SSE)           │  │  │   │  │
│  │  │  │  └─────────────────────────────────────┘  │ │   │  │
│  │  │  └─────────────────────────────────────────┘ │   │  │
│  │  └──────────────────────────────────────────────┘   │  │
│  └────────────────────────────────────────────────────┘  │
│                                                           │
│  ┌────────────────────────────────────────────────────┐  │
│  │  EventSource('/events')                             │  │
│  │  ◄── SSE: data: <log line>                         │  │
│  │  ◄── SSE: event: connected → {logPath, clientCount} │  │
│  └────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────┘
                         │
                         │ HTTP
                         ▼
              ┌─────────────────────┐
              │  httptail Go server  │
              │  :8080               │
              │  /        → static   │
              │  /events  → SSE      │
              └─────────────────────┘
```

### Data flow:

1. **Page load** — `index.html` loads `main.jsx` as an ES module. React mounts `<App />` into `#root`.
2. **SSE connect** — `useEffect` on mount creates `new EventSource('/events')`, registers event handlers.
3. **History batch** — The server sends the last N log lines as `data:` events, then a `connected` event with metadata.
4. **Live streaming** — Each new log line from the server arrives as an `onmessage` event, is appended to the `logs` state array, and the display auto-scrolls to the bottom.
5. **User interaction** — Pause stops auto-scroll (user can scroll freely). Clear empties the log array. Both are immediate; streaming continues in the background.
6. **Disconnect / reconnect** — The browser's `EventSource` auto-reconnects. Status indicator shows current connection state. On reconnect, the server re-sends history.

---

## 2. Project Files

| File | Role |
|---|---|
| `index.html` | HTML entry point — mounts React into `<div id="root">` |
| `package.json` | Node.js project manifest — dependencies, scripts |
| `vite.config.js` | Vite build configuration — React plugin |
| `eslint.config.js` | ESLint flat config — React hooks and refresh rules |
| `.gitignore` | Git ignore rules — node_modules, dist, editor files |
| `src/main.jsx` | React application — sole component file, SSE client, entry point |
| `src/styles.css` | All application styles — fonts, dark theme, layout, components |
| `src/OpenSans-Regular.ttf` | Open Sans Regular (weight 400) |
| `src/OpenSans-SemiBold.ttf` | Open Sans SemiBold (weight 600) |
| `src/OpenSans-Bold.ttf` | Open Sans Bold (weight 700) |
| `README.md` | Vite scaffold README (generic, not project-specific) |

---

## 3. Build & Tooling Configuration

### 3.1 `package.json`

```json
{
  "name": "http-tail",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "lint": "eslint .",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^19.2.6",
    "react-dom": "^19.2.6"
  },
  "devDependencies": {
    "@eslint/js": "^10.0.1",
    "@types/react": "^19.2.14",
    "@types/react-dom": "^19.2.3",
    "@vitejs/plugin-react": "^6.0.1",
    "eslint": "^10.3.0",
    "eslint-plugin-react-hooks": "^7.1.1",
    "eslint-plugin-react-refresh": "^0.5.2",
    "globals": "^17.6.0",
    "vite": "^8.0.12"
  }
}
```

**Key details:**

| Field | Value | Purpose |
|---|---|---|
| `"type": "module"` | `"module"` | Enables ES module `import`/`export` syntax (`.jsx` files use `import`, not `require`) |
| `"private": true` | `true` | Prevents accidental `npm publish` — this is an application, not a library |
| `react` | `^19.2.6` | React 19 — UI library (function components, hooks, StrictMode) |
| `react-dom` | `^19.2.6` | React DOM 19 — `createRoot` API for React 18+ concurrent rendering |
| `vite` | `^8.0.12` | Vite 8 — dev server with HMR, production bundler (Rollup-based) |
| `@vitejs/plugin-react` | `^6.0.1` | Vite React plugin — uses Oxc (not SWC) for JSX transform |
| `eslint` | `^10.3.0` | ESLint 10 — static analysis with flat config format |
| `eslint-plugin-react-hooks` | `^7.1.1` | Enforces Rules of Hooks (`useEffect` dependencies, hook call order) |
| `eslint-plugin-react-refresh` | `^0.5.2` | Ensures components are exported for HMR Fast Refresh compatibility |
| `@types/react` / `@types/react-dom` | `^19.2.x` | TypeScript type definitions (present for IDE support; the project uses `.jsx`, not `.tsx`) |

**NPM scripts:**

| Script | Command | Purpose |
|---|---|---|
| `dev` | `vite` | Start Vite dev server with HMR (default port 5173) |
| `build` | `vite build` | Production build — outputs to `dist/` |
| `lint` | `eslint .` | Lint all `.js`/`.jsx` files against the flat config |
| `preview` | `vite preview` | Serve the production build locally for testing |

### 3.2 `vite.config.js`

```js
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
})
```

**Key details:**
- **Single plugin**: `@vitejs/plugin-react` — handles JSX transform via Oxc (faster than Babel/SWC for most cases)
- **No custom `base` path** — assets are referenced from root (`/`), compatible with the Go server serving from `/`
- **No dev server proxy** — during development with `npm run dev`, the Vite dev server runs on port 5173 and the SSE endpoint would need to be proxied (currently not configured — see §10.1)
- **No custom `build.outDir`** — defaults to `dist/`, which the Go server's `-frontend` flag expects (default `web/http-tail/dist`)

### 3.3 `eslint.config.js`

```js
import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{js,jsx}'],
    extends: [
      js.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
      parserOptions: { ecmaFeatures: { jsx: true } },
    },
  },
])
```

**Key details:**
- **Flat config format** (ESLint 10) — uses `defineConfig` and array-based configuration
- **`globalIgnores(['dist'])`** — skips the production build output directory
- **`files: ['**/*.{js,jsx}']`** — lints JavaScript and JSX files only
- **`js.configs.recommended`** — core ESLint recommended rules
- **`reactHooks.configs.flat.recommended`** — enforces Rules of Hooks (hook calling order, exhaustive deps)
- **`reactRefresh.configs.vite`** — requires component exports for HMR Fast Refresh (disabled in `main.jsx` via `/* eslint-disable react-refresh/only-export-components */` because it's the entry point)
- **`globals.browser`** — declares browser globals (`window`, `document`, `EventSource`, `console`, etc.)
- **`ecmaFeatures: { jsx: true }`** — enables JSX syntax parsing

### 3.4 `index.html`

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>http-tail</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.jsx"></script>
  </body>
</html>
```

**Key details:**
- **`<div id="root">`** — the React mount point. `createRoot(document.getElementById('root'))` targets this element.
- **`<script type="module" src="/src/main.jsx">`** — Vite transforms this during dev/build. The `type="module"` enables ES module features.
- **`<meta name="viewport" ...>`** — enables responsive scaling on mobile devices.
- **`<link rel="icon" ...>`** — references `/favicon.svg` (not present in production; the Go server would need to provide one or this returns 404).
- **`<title>http-tail</title>`** — browser tab title.

### 3.5 `.gitignore`

```
# Logs
logs
*.log
npm-debug.log*
yarn-debug.log*
yarn-error.log*
pnpm-debug.log*
lerna-debug.log*

node_modules
dist
dist-ssr
*.local

# Editor directories and files
.vscode/*
!.vscode/extensions.json
.idea
.DS_Store
*.suo
*.ntvs*
*.njsproj
*.sln
*.sw?
```

Key entries for this project:
- `node_modules` — dependencies installed by npm
- `dist` — production build output (generated by `npm run build`)
- `dist-ssr` — SSR build output (not used)
- Editor/OS files (`.vscode`, `.idea`, `.DS_Store`, etc.)

---

## 4. Source: `main.jsx`

**File path**: `src/main.jsx`
**Lines**: 182
**Role**: The entire React application — component definition, SSE client, event handling, rendering, and DOM mounting. This is the sole JavaScript source file.

```
Line   1:  /* eslint-disable react-refresh/only-export-components */
Line   3:  import { StrictMode, useState, useEffect, useRef, useCallback } from 'react'
Line   4:  import { createRoot } from 'react-dom/client'
Line   5:  import './styles.css'
Line   7:  const MAX_LOG_LINES = 10000
Line  10:  function App() { ... }        // lines 10–175
Line 177:  createRoot(document.getElementById('root')).render(...)
```

### 4.1 Imports

```js
import { StrictMode, useState, useEffect, useRef, useCallback } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'
```

| Import | Source | Used For |
|---|---|---|
| `StrictMode` | `react` | Wraps `<App />` in development to detect side-effects and potential issues by double-invoking effects/reducers |
| `useState` | `react` | 5 state variables: `logs`, `status`, `paused`, `logPath`, `clientCount` |
| `useEffect` | `react` | 3 effects: pause ref sync, SSE connection lifecycle, auto-scroll |
| `useRef` | `react` | 3 refs: `logRef` (DOM element), `esRef` (EventSource instance), `pausedRef` (mutable pause value) |
| `useCallback` | `react` | 2 memoized handlers: `handleClear`, `handleTogglePause` |
| `createRoot` | `react-dom/client` | React 18+ concurrent root API — mounts the component tree into the DOM |
| `'./styles.css'` | local | Side-effect import — injects the stylesheet into the bundle |

### 4.2 Constants

```js
const MAX_LOG_LINES = 10000
```

| Constant | Type | Value | Used In | Purpose |
|---|---|---|---|---|
| `MAX_LOG_LINES` | `number` | `10000` | SSE `onmessage` handler | Maximum number of log lines retained in the `logs` state array. When exceeded, the array is trimmed to half this value (`5000`) to bound memory usage. Prevents the browser tab from consuming unbounded memory during long-running tail sessions. |

This is a module-level constant (not inside the `App` function), so it is defined once at module parse time and shared across all renders.

### 4.3 Component: `App`

```js
function App() { ... }
```

The sole React component. A regular function component (not arrow function, not `memo`-wrapped). Contains all state, effects, handlers, and the render tree.

**No props** — this is the root component; it receives no external data. All data arrives via SSE at runtime.

#### 4.3.1 State Variables

| Variable | Setter | Initial Value | Type | Purpose |
|---|---|---|---|---|
| `logs` | `setLogs` | `[]` | `string[]` | Array of log lines displayed in the scroll area. Append-only during normal streaming; replaced with `[]` on clear; trimmed to `MAX_LOG_LINES/2` when the cap is exceeded. |
| `status` | `setStatus` | `'connecting'` | `'connecting' \| 'connected' \| 'disconnected'` | Current SSE connection state. Drives the status dot color (green/yellow/red), status label text, and the pulse animation on warning/error states. |
| `paused` | `setPaused` | `false` | `boolean` | Whether auto-scroll is paused. When `true`, new log lines still arrive and are appended to `logs`, but the viewport does not scroll to follow them. Toggled by the Pause/Resume button. |
| `logPath` | `setLogPath` | `'Connecting...'` | `string` | Path of the log file being tailed. Set from the SSE `connected` event's `logPath` field. Displayed in the header bar. |
| `clientCount` | `setClientCount` | `0` | `number` | Number of connected SSE clients. Set from the SSE `connected` event's `clientCount` field. Displayed in the status indicator when > 1. |

#### 4.3.2 Refs

| Ref | Initial Value | Type | Purpose |
|---|---|---|---|
| `logRef` | `null` | `{ current: HTMLDivElement \| null }` | Attached to the `.log-area` `<main>` element via `ref={logRef}`. Read in the auto-scroll effect to set `scrollTop = scrollHeight`. |
| `esRef` | `null` | `{ current: EventSource \| null }` | Holds the active `EventSource` instance. Used in the SSE effect to close the previous connection before opening a new one (though with `[]` deps this only matters for StrictMode double-mount), and in the cleanup function to close the connection on unmount. |
| `pausedRef` | `false` | `{ current: boolean }` | Mutable mirror of the `paused` state. Updated by a `useEffect` whenever `paused` changes. Read in the auto-scroll effect instead of `paused` directly, so the auto-scroll effect does not need `paused` in its dependency array and only re-runs when `logs` change (via the no-deps-array trigger-on-every-render pattern). |

#### 4.3.3 Effect: Pause Ref Sync

```js
useEffect(() => {
    pausedRef.current = paused
}, [paused])
```

**Trigger**: Runs whenever `paused` state changes.
**Dependency array**: `[paused]`
**Purpose**: Keeps `pausedRef.current` synchronized with the `paused` state value.

**Why a ref is needed**: The auto-scroll effect (§4.3.5) uses a no-dependency-array pattern (runs after every render). If it read `paused` directly, React's closure semantics would capture the `paused` value from the render that *triggered* the effect registration, not the current render. By reading `pausedRef.current` instead, the effect always sees the latest value. This is the standard React pattern for "latest value without re-subscribing."

#### 4.3.4 Effect: SSE Connection

```js
useEffect(() => {
    function connect() { ... }
    connect()
    return () => { if (esRef.current) esRef.current.close() }
}, [])
```

**Trigger**: Runs once on mount (empty dependency array `[]`).
**Cleanup**: Runs on unmount — closes the `EventSource` connection.

The `connect()` function inside the effect:

1. **Close previous connection** — `if (esRef.current) esRef.current.close()`. In production (no StrictMode), this is a no-op on first mount because `esRef.current` is `null`. In development StrictMode, React double-mounts the component (mount → unmount → mount), so the second mount's `connect()` call closes the first mount's EventSource before creating a new one. This prevents duplicate SSE connections.

2. **Set connecting status** — `setStatus('connecting')` updates the UI to show a yellow pulsing dot.

3. **Create EventSource** — `const es = new EventSource('/events')`. Creates a persistent HTTP connection to the SSE endpoint on the same origin. The browser automatically handles reconnection with exponential backoff.

4. **Register `onopen` handler**:
   ```js
   es.onopen = () => { setStatus('connected') }
   ```
   Fires when the TCP connection is established (not when data arrives). Sets status to `'connected'` (green dot).

5. **Register `connected` event listener**:
   ```js
   es.addEventListener('connected', (e) => {
       try {
           const data = JSON.parse(e.data)
           if (data.logPath) setLogPath(data.logPath)
           if (data.clientCount !== undefined) setClientCount(data.clientCount)
       } catch { /* ignore malformed JSON */ }
       setStatus('connected')
   })
   ```
   Listens for the named SSE `connected` event that the server sends after the history batch. Parses the JSON payload:
   - `data.logPath` → updates `logPath` state (displayed in the header)
   - `data.clientCount` → updates `clientCount` state (displayed when > 1)
   - Sets status to `'connected'` (may already be set by `onopen`, but this confirms the full handshake is complete)

   The `try/catch` silently ignores malformed JSON — metadata is non-critical; the log streaming continues regardless.

6. **Register `onmessage` handler**:
   ```js
   es.onmessage = (e) => {
       const line = e.data
       setLogs((prev) => {
           const next = [...prev, line]
           if (next.length > MAX_LOG_LINES) {
               return next.slice(-Math.floor(MAX_LOG_LINES / 2))
           }
           return next
       })
   }
   ```
   Fires for each unnamed `data:` SSE event (each log line).
   - Uses the **functional updater** form of `setLogs` (`(prev) => newState`) to avoid depending on the `logs` state value in the closure. This is critical because the effect only runs once (empty deps), so the `logs` value captured at mount time would always be `[]`.
   - **Memory cap**: If appending the line would exceed `MAX_LOG_LINES` (10,000), the array is sliced to the most recent `MAX_LOG_LINES / 2` (5,000) entries. This is not a strict cap — the array can grow to 10,001 before trimming — but the trim operation runs synchronously inside the state update, so the in-memory size never exceeds 10,001 entries before dropping to 5,000.

7. **Register `onerror` handler**:
   ```js
   es.onerror = () => {
       setStatus('disconnected')
       // Do NOT close the EventSource — let the browser retry.
   }
   ```
   Fires when the SSE connection drops (network error, server restart, etc.).
   - Sets status to `'disconnected'` (red pulsing dot).
   - **Does NOT close the EventSource.** The browser's `EventSource` implementation automatically attempts reconnection with exponential backoff. When reconnection succeeds, `onopen` fires again, and the server sends the history batch + `connected` event. This means the client seamlessly recovers from transient disconnections.

**Dependencies**: `[]` — the effect runs only on mount/unmount. All state updates inside use functional updaters or refs to avoid stale closures.

#### 4.3.5 Effect: Auto-Scroll

```js
useEffect(() => {
    if (!pausedRef.current && logRef.current) {
        logRef.current.scrollTop = logRef.current.scrollHeight
    }
})
```

**Trigger**: Runs **after every render** (no dependency array — not `[]`, but absent entirely).
**Purpose**: Scrolls the `.log-area` element to the bottom whenever new log lines are rendered, unless the user has paused scrolling.

**Why no dependency array**: This is the "run after every render" pattern. The effect needs to fire whenever `logs` changes (which causes a re-render). Rather than depending on `[logs]`, it uses no deps array, which means it runs after every render regardless of what changed. This is intentional:
- It reads `pausedRef.current` (a ref) instead of `paused`, so it always sees the latest pause state without needing `paused` in the dependency array.
- It reads `logRef.current` (a ref to the DOM element), which is set once and never changes.
- Setting `scrollTop = scrollHeight` is idempotent when already scrolled to the bottom — it doesn't cause additional re-renders.

**Pause behavior**: When `pausedRef.current` is `true`, the effect is a no-op. The user can freely scroll up to inspect earlier lines without being "fought" by auto-scroll. New lines continue to arrive and append to the DOM, but the viewport stays where the user left it.

**Resume behavior**: When the user clicks Resume (`paused` → `false`), the effect runs on the next render and scrolls to the bottom, catching the user up to the latest line.

#### 4.3.6 Handlers

```js
const handleClear = useCallback(() => {
    setLogs([])
}, [])

const handleTogglePause = useCallback(() => {
    setPaused((prev) => !prev)
}, [])
```

| Handler | Wrapped With | Dependencies | Behavior |
|---|---|---|---|
| `handleClear` | `useCallback` | `[]` (never changes) | Resets `logs` to an empty array `[]`. The log display area shows the empty state message. The SSE connection is unaffected — new lines continue to arrive and populate the array. |
| `handleTogglePause` | `useCallback` | `[]` (never changes) | Toggles `paused` between `true` and `false` using the functional updater `(prev) => !prev`. Updates `pausedRef.current` via the pause ref sync effect (§4.3.3). The button label switches between `'▶ Resume'` and `'⏸ Pause'`. |

Both handlers use `useCallback` with empty dependency arrays, so their references are stable for the component's lifetime — they never cause unnecessary re-renders of child elements.

#### 4.3.7 Status Display Map

```js
const statusLabel = {
    connected: 'Connected',
    connecting: 'Connecting...',
    disconnected: 'Disconnected',
}
```

A plain object mapping the three `status` state values to human-readable display strings. Defined inside the component but outside of any hook/callback — it is re-created on every render, but as a small constant object with 3 keys, the cost is negligible. Could be hoisted to module scope for optimization.

#### 4.3.8 Render Tree

```jsx
<div className="app">
    <header className="header">
        <div className="header-left">
            <span className="header-title">&gt; tail</span>
            <span className="header-path" title={logPath}>
                {logPath}
            </span>
        </div>
        <div className="header-right">
            <span className="status-indicator">
                <span className={`status-dot status-${status}`} />
                <span className="status-label">
                    {statusLabel[status] || status}
                </span>
                {clientCount > 1 && (
                    <span className="status-clients">
                        ({clientCount} connected)
                    </span>
                )}
            </span>
            <button className="btn" onClick={handleTogglePause} type="button">
                {paused ? '▶ Resume' : '⏸ Pause'}
            </button>
            <button className="btn btn-clear" onClick={handleClear} type="button">
                &#x2715; Clear
            </button>
        </div>
    </header>

    <main className="log-area" ref={logRef}>
        {logs.length === 0 && (
            <div className="log-empty">
                {status === 'connected'
                    ? 'Waiting for log entries...'
                    : 'Connecting to server...'}
            </div>
        )}
        {logs.map((line, i) => (
            <div key={i} className="log-line">
                {line}
            </div>
        ))}
    </main>
</div>
```

**Structure breakdown:**

| Element | Class | Purpose |
|---|---|---|
| `div` | `app` | Root container — flex column, full viewport height |
| `header` | `header` | Fixed top bar — dark background, flex row, space-between |
| `div` | `header-left` | Left group — title + file path |
| `span` | `header-title` | `> tail` — brand text in accent blue. Hardcoded string. |
| `span` | `header-path` | Log file path from `logPath` state. Monospace font, text-overflow ellipsis for long paths. `title` attribute shows the full path on hover. |
| `div` | `header-right` | Right group — status + buttons |
| `span` | `status-indicator` | Flex container for the status dot + label + client count |
| `span` | `status-dot status-{status}` | 8×8px colored circle — green (`connected`), yellow with pulse (`connecting`), red with pulse (`disconnected`). Dynamic class via template literal. |
| `span` | `status-label` | Text label from `statusLabel` map. |
| `span` | `status-clients` | Client count text — only rendered when `clientCount > 1`. Shows `(N connected)`. |
| `button` | `btn` | Pause/Resume toggle — label changes based on `paused` state. `type="button"` prevents accidental form submission. |
| `button` | `btn btn-clear` | Clear button — `✕ Clear` text. Hover turns border/color to `--error` (red). |
| `main` | `log-area` | Scrollable log display — `ref={logRef}` for scroll control. Flex-1 fills remaining height. Monospace font. |
| `div` | `log-empty` | Conditional empty state — shown only when `logs.length === 0`. Message changes based on `status`: "Waiting for log entries..." (connected) or "Connecting to server..." (connecting/disconnected). |
| `div` | `log-line` | Individual log line — rendered via `logs.map()`. Uses array index as `key`. `white-space: pre-wrap` preserves whitespace. Bottom border for visual separation. |

**Key rendering details:**
- **`key={i}`**: Uses the array index as the React key. This is acceptable because lines are never inserted, removed, or re-ordered — they only append or are entirely cleared. If lines were filtered or sorted, a stable unique ID from the server would be required.
- **Dynamic status class**: `` className={`status-dot status-${status}`} `` maps to `.status-connected`, `.status-connecting`, or `.status-disconnected` in CSS.
- **Empty state logic**: When `logs` is empty (initial load or after clear), the `.log-empty` placeholder is rendered instead of an empty scroll area. The message is contextual: connected but waiting vs still connecting.
- **Line content**: `{line}` renders the raw log line text. React automatically escapes HTML entities in text content, so log lines containing HTML tags are safe (rendered as text, not parsed as HTML).

### 4.4 Entry Point

```js
createRoot(document.getElementById('root')).render(
    <StrictMode>
        <App />
    </StrictMode>,
)
```

| API | Purpose |
|---|---|
| `document.getElementById('root')` | Finds the mount point `<div>` from `index.html` |
| `createRoot(...)` | React 18+ concurrent root — enables concurrent rendering features |
| `.render(...)` | Mounts the React element tree into the DOM |
| `<StrictMode>` | Development-only wrapper that double-invokes effects and state updaters to detect side-effect bugs. Stripped in production builds. |
| `<App />` | The root component (§4.3) |

**Note**: In React 18+, `ReactDOM.render()` is deprecated. The project uses the correct `createRoot` API. There is no `ReactDOM` import — only `createRoot` is imported from `react-dom/client`.

---

## 5. Source: `styles.css`

**File path**: `src/styles.css`
**Lines**: 247
**Role**: All application styles — font declarations, CSS custom properties (design tokens), global reset, layout, and component-specific styles. No CSS modules, no preprocessor, no utility framework — plain CSS with custom properties.

### 5.1 `@font-face` Declarations

Three `@font-face` rules load the self-hosted Open Sans font files:

| Declaration | Weight | File | Format |
|---|---|---|---|
| `font-family: 'Open Sans'` | 400 (Regular) | `OpenSans-Regular.ttf` | `truetype` |
| `font-family: 'Open Sans'` | 600 (SemiBold) | `OpenSans-SemiBold.ttf` | `truetype` |
| `font-family: 'Open Sans'` | 700 (Bold) | `OpenSans-Bold.ttf` | `truetype` |

All three use `font-display: swap` — text is rendered immediately with a fallback font while the custom font loads, then swapped in when ready. This prevents the "flash of invisible text" (FOIT) on slow connections. The font files are relative to `styles.css` (`./`), so they are resolved from `src/` and bundled by Vite during the build.

**Usage in the app:**
- **400 (Regular)**: Body text, status labels, buttons, empty state
- **600 (SemiBold)**: Navigation items, button text (via `font-weight: 600`)
- **700 (Bold)**: Header title (`> tail`), sidebar title

### 5.2 CSS Custom Properties

14 design tokens defined on `:root` (global scope):

| Variable | Value | Role |
|---|---|---|
| `--accent` | `#4fc3f7` | Primary accent — light blue. Used for header title, active nav items, selection background, button hover border. |
| `--accent-hover` | `#29b6f6` | Darker accent — defined but **not currently used** in the http-tail stylesheet. Available for future hover states. |
| `--bg-primary` | `#27374d` | Primary background — dark navy. Page background, scrollbar track, selection text color. |
| `--bg-secondary` | `#526d82` | Secondary background — muted slate. Used via `color-mix()` for header/sidebar backgrounds. |
| `--bg-surface` | `#526d82` | Surface background — same value as `--bg-secondary`. Used via `color-mix()` for button and log-line hover states. |
| `--border` | `#9db2bf` | Border color — light slate. Header bottom border, button borders, log-line separators. |
| `--error` | `#ef5350` | Error/semantic red. Disconnected status dot, clear button hover state. |
| `--scrollbar-thumb` | `#9db2bf` | Custom scrollbar thumb color — same as `--border`. |
| `--scrollbar-track` | `#27374d` | Custom scrollbar track color — same as `--bg-primary`. |
| `--success` | `#66bb6a` | Success/semantic green. Connected status dot. |
| `--text-muted` | `#9db2bf` | Muted text color — light slate. Header path, status indicator, empty state, scrollbar hover. |
| `--text-primary` | `#dde6ed` | Primary text color — near-white. Body text, log lines, button text. |
| `--text-secondary` | `#9db2bf` | Secondary text — same value as `--text-muted`. Defined for consistency with `web/console/`. |
| `--warning` | `#ffa726` | Warning/semantic orange. Connecting status dot. |

**Design notes:**
- These are the exact same 14 variables used in `web/console/src/styles.css`, ensuring visual consistency between the two applications.
- `--bg-secondary` and `--bg-surface` are identical (`#526d82`). They exist as separate tokens so they can diverge in the future (e.g., a lighter surface on dark backgrounds).
- `--text-muted` and `--text-secondary` are identical (`#9db2bf`). Same pattern — semantic distinction for future customization.
- `--accent-hover` is defined but unused in this app; available for extensions.
- All variables are used via `var(--name)` syntax throughout the stylesheet, enabling theme switching by overriding `:root` (see §10.4).

### 5.3 Global Reset & Base Styles

```css
*, *::before, *::after {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
}

html, body, #root {
    height: 100%;
}

body {
    background-color: var(--bg-primary);
    color: var(--text-primary);
    font-family: 'Open Sans', system-ui, -apple-system, sans-serif;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
}
```

| Rule | Purpose |
|---|---|
| Universal `box-sizing: border-box` | Padding and border are included in element width/height calculations — standard modern CSS reset |
| Universal `margin: 0; padding: 0` | Removes all default browser margins and padding |
| `html, body, #root { height: 100% }` | Ensures the root elements fill the viewport height, enabling the flex-column layout to occupy the full window |
| `body` background/text color | Applies the dark theme globally |
| `font-family` stack | Open Sans → system-ui → -apple-system → sans-serif. Graceful fallback if custom fonts fail to load |
| `-webkit-font-smoothing: antialiased` | Enables subpixel antialiasing on macOS for crisper text rendering |

### 5.4 Scrollbar Styling

```css
::-webkit-scrollbar           { height: 8px; width: 8px; }
::-webkit-scrollbar-track     { background: var(--scrollbar-track); }
::-webkit-scrollbar-thumb     { background: var(--scrollbar-thumb); border-radius: 4px; }
::-webkit-scrollbar-thumb:hover { background: var(--text-muted); }

::selection {
    background: var(--accent);
    color: var(--bg-primary);
}
```

- **WebKit-only** (`::-webkit-scrollbar`): Custom dark scrollbars on Chrome, Edge, Safari, and Opera. Firefox ignores these and uses its own `scrollbar-color` property (not set — Firefox will show default scrollbars).
- **8px width/height**: Thin scrollbars that don't dominate the UI.
- **Rounded thumb**: `border-radius: 4px` on the scrollbar thumb for a softer appearance.
- **Hover state**: Thumb color changes from `--scrollbar-thumb` to `--text-muted` on hover.
- **Selection**: Text selection uses accent blue background with dark text (inverted from normal). Consistent across the entire app.

### 5.5 Layout

```css
.app {
    display: flex;
    flex-direction: column;
    height: 100%;
}
```

The root layout is a vertical flexbox filling the viewport:
- **`flex-direction: column`** — stacks the header (fixed height) on top of the log area (flexible).
- **`height: 100%`** — inherits from `#root` → `body` → `html`, all at 100% viewport height.
- The header has `flex-shrink: 0` (does not shrink), and the log area has `flex: 1` (takes remaining space).

### 5.6 Header Bar

```css
.header {
    align-items: center;
    background: color-mix(in srgb, var(--bg-secondary), black 60%);
    border-bottom: 1px solid var(--border);
    display: flex;
    flex-shrink: 0;
    justify-content: space-between;
    padding: 0.5rem 1rem;
    gap: 1rem;
}
```

| Property | Value | Purpose |
|---|---|---|
| `display: flex` | flex | Horizontal layout for left/right groups |
| `justify-content` | `space-between` | Pushes `.header-left` to the left edge and `.header-right` to the right edge |
| `align-items` | `center` | Vertically centers header content |
| `flex-shrink` | `0` | Prevents the header from shrinking when the viewport is short |
| `background` | `color-mix(in srgb, --bg-secondary, black 60%)` | Darkens the secondary background by mixing with 60% black, creating a darker shade than the main background for visual hierarchy |
| `border-bottom` | `1px solid var(--border)` | Subtle separator between header and log area |
| `padding` | `0.5rem 1rem` | Compact vertical padding, comfortable horizontal padding |
| `gap` | `1rem` | Spacing between left and right groups when they wrap (though wrapping is prevented by `flex-shrink: 0` on child groups) |

**Sub-elements:**

`.header-left`: Flex container with `gap: 1rem` and `min-width: 0` (enables text-overflow ellipsis on the path).
`.header-title`: Accent blue, bold (700), `1rem`, `white-space: nowrap`.
`.header-path`: Muted, monospace, `0.8rem`, `text-overflow: ellipsis` with `overflow: hidden` and `white-space: nowrap` — long paths are truncated with "...".
`.header-right`: Flex container with `gap: 0.5rem` and `flex-shrink: 0` — never wraps or shrinks.

### 5.7 Status Indicator

```css
.status-indicator {
    align-items: center; display: flex;
    font-size: 0.8rem; gap: 0.35rem;
    color: var(--text-muted);
}

.status-dot {
    border-radius: 50%; display: inline-block;
    flex-shrink: 0; height: 8px; width: 8px;
}

.status-connected  { background: var(--success); }
.status-connecting { background: var(--warning); animation: pulse 1s ease-in-out infinite; }
.status-disconnected { background: var(--error);    animation: pulse 1s ease-in-out infinite; }

@keyframes pulse {
    0%, 100% { opacity: 1; }
    50%      { opacity: 0.4; }
}
```

| Element | Behavior |
|---|---|
| `.status-dot` | 8×8px circle. `flex-shrink: 0` prevents squishing. |
| `.status-connected` | Solid green (`--success`). No animation — stable state. |
| `.status-connecting` | Yellow (`--warning`) with pulsing opacity (1 → 0.4 → 1 over 1s). Communicates "in progress." |
| `.status-disconnected` | Red (`--error`) with the same pulsing animation. Communicates "error state, attempting recovery." |
| `@keyframes pulse` | Simple opacity oscillation. `ease-in-out` timing for smooth transitions. |

**Dynamic class application**: The React component applies the class via template literal `` status-${status} ``, so only one of `.status-connected`, `.status-connecting`, or `.status-disconnected` is active at a time.

### 5.8 Buttons

```css
.btn {
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    cursor: pointer;
    font-family: 'Open Sans', system-ui, -apple-system, sans-serif;
    font-size: 0.8rem;
    font-weight: 600;
    padding: 0.3rem 0.6rem;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
    white-space: nowrap;
}

.btn:hover {
    background: color-mix(in srgb, var(--bg-surface), black 40%);
    border-color: var(--accent);
}

.btn-clear:hover {
    border-color: var(--error);
    color: var(--error);
}
```

| State | All `.btn` | `.btn-clear` override |
|---|---|---|
| Default | Transparent background, `--border` border, `--text-primary` text | Same |
| Hover | Darkened surface background (`color-mix` with 40% black), `--accent` border | `--error` border + `--error` text (red) |

**Design decisions:**
- **`background: transparent`** by default — buttons don't look like solid blocks, keeping the header clean.
- **`transition: 0.15s`** — smooth hover state changes without feeling sluggish.
- **`font-weight: 600`** — SemiBold for button text, matching the Open Sans weight defined in `@font-face`.
- **`type="button"`** in JSX — prevents accidental form submission if the app is ever wrapped in a `<form>`.
- **Clear button hover is red** — provides a subtle warning affordance for the destructive "clear" action without being aggressive.

### 5.9 Log Display Area

```css
.log-area {
    flex: 1;
    font-family: 'Courier New', Courier, 'Liberation Mono', monospace;
    font-size: 0.82rem;
    line-height: 1.6;
    overflow-y: auto;
}

.log-line {
    border-bottom: 1px solid color-mix(in srgb, var(--border), transparent 88%);
    padding: 0.1rem 1rem;
    white-space: pre-wrap;
    word-break: break-all;
}

.log-line:hover {
    background: color-mix(in srgb, var(--bg-surface), transparent 70%);
}
```

| Property | `.log-area` | `.log-line` |
|---|---|---|
| **Sizing** | `flex: 1` — fills remaining height | Minimal vertical padding (`0.1rem`), horizontal padding for readability |
| **Font** | Monospace stack: Courier New → Courier → Liberation Mono → system monospace. `0.82rem` — slightly smaller than body text, optimized for log density. | Inherits monospace |
| **Line height** | `1.6` — generous spacing for readability of dense log output | Inherits |
| **Overflow** | `overflow-y: auto` — scrollbar appears only when needed | — |
| **Wrapping** | — | `white-space: pre-wrap` — preserves spaces and newlines in log lines, wraps long lines. `word-break: break-all` — breaks extremely long tokens (e.g. base64, long URLs) to prevent horizontal overflow |
| **Border** | — | Subtle separator: `--border` mixed with 88% transparency (almost invisible). Provides visual grouping without heavy "lines between every row" look |
| **Hover** | — | Light highlight using `--bg-surface` mixed with 70% transparency. Helps the eye track a specific line |

### 5.10 Empty State

```css
.log-empty {
    align-items: center;
    color: var(--text-muted);
    display: flex;
    height: 100%;
    justify-content: center;
    font-family: 'Open Sans', system-ui, -apple-system, sans-serif;
}
```

Centers the placeholder text both horizontally and vertically within the log area. Uses the sans-serif font stack (not monospace) to distinguish it from actual log content. Muted color keeps it visually subordinate — it's a hint, not a primary element.

---

## 6. Static Assets

### Font Files

| File | Weight | Format | Size (approx.) |
|---|---|---|---|
| `src/OpenSans-Regular.ttf` | 400 | TrueType | ~122 KB |
| `src/OpenSans-SemiBold.ttf` | 600 | TrueType | ~122 KB |
| `src/OpenSans-Bold.ttf` | 700 | TrueType | ~122 KB |

These are **copies** (not symlinks) of the font files in `web/console/src/fonts/`. They are resolved relative to `styles.css` via `url('./OpenSans-...ttf')` and processed by Vite during the build — Vite copies them into `dist/assets/` with content-hashed filenames (e.g., `OpenSans-Regular-BCdjR_up.ttf`).

### Favicon

Referenced in `index.html` as `/favicon.svg` but **not present** in the source tree. The browser receives a 404 for this resource. This is harmless — the browser tab simply shows the default "no favicon" icon.

---

## 7. SSE Protocol Integration

The frontend consumes the following SSE protocol from the `httptail` Go server:

### Event Reference

| Event Type | SSE Format | Direction | Trigger | Handled By |
|---|---|---|---|---|
| Unnamed `data:` | `data: <escaped-line>\n\n` | Server → Client | Server sends history lines on connect, then each new log line | `es.onmessage` |
| Named `connected` | `event: connected\ndata: {"logPath":"...","clientCount":N}\n\n` | Server → Client | Server sends once after history batch on each new connection | `es.addEventListener('connected', ...)` |

### Lifecycle Sequence

```
Client                            Server
  │                                  │
  │──── new EventSource('/events') ──▶│
  │                                  │
  │◀─── data: <history line 1> ──────│  (history batch)
  │◀─── data: <history line 2> ──────│
  │◀─── ...                          │
  │◀─── data: <history line N> ──────│
  │                                  │
  │◀─── event: connected ────────────│  (metadata)
  │     data: {"logPath":"...",      │
  │            "clientCount":N}       │
  │                                  │
  │     [es.onopen fires]            │  (connection confirmed)
  │     [status → 'connected']       │
  │                                  │
  │◀─── data: <new log line> ────────│  (live streaming)
  │◀─── data: <new log line> ────────│
  │◀─── ...                          │
  │                                  │
  │         ~~~ connection lost ~~~   │
  │     [es.onerror fires]           │
  │     [status → 'disconnected']    │
  │                                  │
  │         ~~~ browser reconnects ~~│
  │──── new EventSource('/events') ──▶│
  │◀─── history batch + connected ───│  (full cycle repeats)
```

### Data Flow Through React State

```
SSE data: line
      │
      ▼
es.onmessage handler
      │
      ▼
setLogs((prev) => [...prev, line])    // functional updater
      │
      ▼
React re-renders <App />
      │
      ▼
logs.map((line, i) => <div key={i} className="log-line">{line}</div>)
      │
      ▼
Auto-scroll effect: logRef.current.scrollTop = logRef.current.scrollHeight
```

```
SSE event: connected
      │
      ▼
es.addEventListener('connected', handler)
      │
      ▼
JSON.parse(e.data) → { logPath, clientCount }
      │
      ├──▶ setLogPath(data.logPath)       → header-path span text
      └──▶ setClientCount(data.clientCount) → status-clients span (if > 1)
```

---

## 8. State Machine

### Connection Status Transitions

```
                    ┌──────────────┐
                    │  connecting  │  (initial state, yellow pulsing dot)
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
         onopen fires   connected     onerror
              │         event fires      fires
              ▼            ▼            ▼
       ┌──────────┐ ┌──────────┐ ┌──────────────┐
       │connected │ │connected │ │ disconnected │
       └────┬─────┘ └──────────┘ └──────┬───────┘
            │          (green dot)       │ (red pulsing dot)
            │                            │
            │ onerror fires              │ browser auto-reconnects
            │ (connection lost)          │ → new EventSource created
            │                            │
            └────────────────────────────┘
                         │
                         ▼
                  ┌──────────────┐
                  │ disconnected │
                  └──────────────┘
```

Notes on transitions:
- `onopen` and the `connected` event are independent — either (or both) can set status to `'connected'`. The `connected` event is the more reliable indicator because it confirms the full SSE handshake (history + metadata sent), whereas `onopen` fires as soon as the TCP connection is established.
- The browser's `EventSource` auto-reconnect is transparent to the React code — a new connection triggers the same `onopen` → `onmessage`/`connected` event sequence as the initial connection.
- `onerror` does NOT close the EventSource. The browser manages reconnection internally.

### Pause State Transitions

```
                    ┌──────────┐
                    │  paused  │
                    │  = false │  (initial state — auto-scroll active)
                    └────┬─────┘
                         │
                    click "⏸ Pause"
                         │
                         ▼
                    ┌──────────┐
                    │  paused  │
                    │  = true  │  (auto-scroll suspended)
                    └────┬─────┘
                         │
                    click "▶ Resume"
                         │
                         ▼
                    ┌──────────┐
                    │  paused  │
                    │  = false │  (auto-scroll resumes, scrolls to bottom)
                    └──────────┘
```

When paused, the auto-scroll effect becomes a no-op. The user can scroll freely. New log lines continue to arrive and append to the DOM (they just don't trigger a scroll). When resumed, the next render scrolls to the bottom, catching up.

---

## 9. Edge Cases Handled

| Scenario | Behavior |
|---|---|
| **First load, no log lines yet** | Empty state shows "Connecting to server..." while connecting, "Waiting for log entries..." when connected but the file is empty |
| **Clear while streaming** | `setLogs([])` empties the display; new lines continue to arrive and populate from scratch |
| **Pause while new lines arrive** | Lines accumulate in the DOM above the visible viewport; resume scrolls to the bottom |
| **Very long log line** | `white-space: pre-wrap` + `word-break: break-all` ensure the line wraps within the viewport without horizontal overflow |
| **Log line containing HTML** | React automatically escapes text content — `<script>alert(1)</script>` is rendered as text, not executed |
| **Log line containing SSE control characters** | The server escapes `\n`, `\r`, `\\` before sending; the frontend receives pre-escaped data |
| **Memory cap (10,000+ lines)** | Array is sliced to 5,000 entries in the `setLogs` functional updater |
| **Browser tab hidden/backgrounded** | SSE continues to receive events; `scrollTop` is set but no visual scroll occurs; when tab returns to foreground, it shows the latest position (or wherever the user left it if paused) |
| **Server restart** | `onerror` fires, status shows "Disconnected", browser auto-reconnects, server sends history batch, UI recovers automatically |
| **Network error** | Same as server restart — EventSource auto-reconnects |
| **Client count update** | Displayed when `> 1`; hidden when only one client (reduces visual noise for solo users) |
| **Malformed connected event JSON** | `try/catch` silently ignores — the log streaming continues unaffected |
| **StrictMode double-mount (dev)** | The previous EventSource is closed before a new one is created in `connect()` |
| **Very long file path** | `text-overflow: ellipsis` truncates with "..."; full path available via `title` attribute on hover |
| **Unmount during streaming** | Cleanup function closes the EventSource, preventing memory leaks |

---

## 10. Extension / Enhancement Points

### 10.1 Development proxy for Vite dev server

Currently, `npm run dev` starts Vite on port 5173, but the SSE `/events` endpoint is on the Go server (port 8080). To make development work seamlessly, add a proxy to `vite.config.js`:

```js
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/events': 'http://localhost:8080',
    },
  },
})
```

This routes SSE requests from the Vite dev server to the Go server while keeping HMR for React components.

### 10.2 Search/filter

- **Client-side filter**: Add a search input in the header. Filter `logs` before the `.map()` to only render matching lines. Keep the full `logs` array intact so clearing the filter restores all lines.
- **Server-side filter**: Add a query parameter to the EventSource URL: `new EventSource('/events?filter=ERROR')`. The Go server would need to support filtering (see `cmd/httptail/REFERENCE.md` §9.3).

### 10.3 Colorized log levels

- **Client-side regex**: In the `onmessage` handler, parse each line with a regex for level keywords (ERROR, WARN, INFO, DEBUG) and wrap in a `<span className="level-error">` or similar. Requires changing from plain string array to an array of `{ text, level }` objects, and updating the render to use `dangerouslySetInnerHTML` or JSX elements.
- **Server-side annotation**: The Go server could parse structured logs and send named SSE events per level (e.g., `event: error`, `event: info`). Listen for these with `es.addEventListener('error', ...)` and apply corresponding CSS classes.

### 10.4 Theme switching (dark/light)

Add CSS custom property overrides for a light theme:

```css
:root.light {
    --bg-primary: #f5f5f5;
    --bg-secondary: #e0e0e0;
    --text-primary: #1a1a1a;
    /* ... override all 14 variables */
}
```

Toggle the `light` class on `<html>` via a button in the header. Requires:
- A `theme` state variable (`'dark'` | `'light'`)
- A `useEffect` that sets `document.documentElement.className = theme`
- CSS variable overrides for all color tokens

### 10.5 Export / download

Add a "Download" button that serializes `logs` to a text file:

```js
const handleDownload = useCallback(() => {
    const blob = new Blob([logs.join('\n')], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url; a.download = 'httptail-export.log'
    a.click()
    URL.revokeObjectURL(url)
}, [logs])
```

### 10.6 Scroll-to-bottom button

When paused and scrolled up, show a floating "↓ New lines" button that appears when new lines arrive and the user is not at the bottom. Clicking it scrolls to the bottom and resumes auto-scroll. Requires tracking `scrollTop` vs `scrollHeight - clientHeight` in a scroll event handler.

### 10.7 Line numbers

Add a line number column using CSS counters or by tracking a `lineNumber` alongside each log entry. Change the `logs` state from `string[]` to `{ id: number, text: string }[]` and render:

```jsx
<div key={line.id} className="log-line">
    <span className="log-line-number">{line.id}</span>
    <span className="log-line-text">{line.text}</span>
</div>
```

### 10.8 Timestamps

If the log format includes timestamps, parse them client-side and display them in a separate column. Use a monospace font for timestamps and a muted color to keep the focus on the log content.

### 10.9 Virtual scrolling

For extremely high-volume logs (100k+ lines in the DOM), current performance will degrade because every line is a DOM node. Implement virtual scrolling using a library like `@tanstack/react-virtual` or `react-window`. This renders only the visible ~30-50 lines and recycles DOM nodes during scroll, maintaining smooth performance at any line count.

### 10.10 Responsive / mobile layout

The current layout works on desktop but has issues on narrow screens:
- Add a media query to stack the header vertically on small screens
- Reduce font sizes and padding
- Make buttons icon-only (no text labels) on mobile
- The log area already handles narrow widths via `word-break: break-all`

### 10.11 Keyboard shortcuts

Add global keyboard shortcuts:
- `Space` — toggle pause/resume
- `Escape` — clear
- `f` — focus search (if search is added)
- `/` — focus search

Use a `useEffect` with `window.addEventListener('keydown', ...)` and check `event.target` to avoid intercepting input field keystrokes.

### 10.12 Multi-file tabs

If the Go server is extended to watch multiple files, add tab navigation in the header. Each tab corresponds to a different EventSource endpoint or query parameter. The `logs` state would become a `{ [filePath]: string[] }` map, with the active tab determining which array is displayed.

### 10.13 Toast notifications

Add a toast/notification system for events:
- "Connected to /var/log/app.log"
- "Connection lost — reconnecting..."
- "Reconnected"
- "3 clients connected"

Use a small notification component that appears in the bottom-right corner and auto-dismisses after a few seconds.

### 10.14 Extract components

The current architecture places the entire app in a single `App` function. For larger enhancements, extract components:

| Component | Extracted From | Props |
|---|---|---|
| `<Header>` | `.header` JSX | `logPath`, `status`, `clientCount`, `paused`, `onTogglePause`, `onClear` |
| `<StatusIndicator>` | `.status-indicator` JSX | `status`, `clientCount` |
| `<LogArea>` | `.log-area` JSX | `logs`, `status`, `logRef` |
| `<LogLine>` | `.log-line` JSX (inside `.map()`) | `line`, `index` |

---

## Appendix: Comparison with `web/console/`

Both frontends share the same DNA but serve different purposes:

| Aspect | `web/console/` | `web/http-tail/` |
|---|---|---|
| **Layout** | 2-column: sidebar (260px) + content area | Single column: header + scrollable log area |
| **Interactivity** | Static nav items (no handlers) | Full interactivity: SSE, pause, clear, auto-scroll |
| **State** | None | 5 state variables, 3 effects, 3 refs |
| **Data source** | None (placeholder content) | Server-Sent Events from `httptail` Go service |
| **CSS variables** | Same 14 tokens | Identical 14 tokens |
| **Fonts** | Open Sans (same 3 weights) | Open Sans (same 3 weights, copied) |
| **Font files location** | `src/fonts/` | `src/` (flat) |
| **Dependencies** | Identical (React 19, Vite 8, ESLint 10) | Identical |
| **Build output** | `dist/` | `dist/` |

The `web/http-tail` is the more mature, interactive application. Patterns established here (SSE client, auto-scroll, status indicator, dark theme CSS variables) can serve as a reference for adding interactivity to `web/console/`.

---

*Generated from `web/http-tail/src/main.jsx`, `src/styles.css`, `index.html`, `package.json`, `vite.config.js`, `eslint.config.js` — keep in sync with code changes.*
