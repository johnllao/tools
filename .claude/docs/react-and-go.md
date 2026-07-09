# React + Go Backend — Same-Origin (No CORS)

Serve everything on one port. The Go backend serves both the API endpoints and the React build files. React calls `/api/...` — same origin, no CORS needed.

## Project Layout

```
project/
├── backend/           # Go backend
│   ├── main.go
│   └── go.mod
├── frontend/          # React (Vite)
│   ├── src/
│   │   └── App.tsx
│   ├── public/
│   ├── package.json
│   └── vite.config.ts
└── run.sh
```

## Go Backend — Serves API + React Static Files

```go
package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed all:dist
var frontend embed.FS

func main() {
	mux := http.NewServeMux()

	// --- API routes (prefixed with /api) ---
	mux.HandleFunc("GET /api/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Hello from Go!",
		})
	})

	mux.HandleFunc("POST /api/data", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"received": body,
			"status":   "ok",
		})
	})

	// --- Serve React static files ---
	// In production, the React build is embedded.
	// The `dist` directory must exist (built from frontend/).
	sub, err := fs.Sub(frontend, "dist")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.Handle("GET /", fileServer)

	// --- Handle SPA client-side routing ---
	// Any non-API, non-file request returns index.html
	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
		// Skip if the file exists
		path := filepath.Clean(r.URL.Path)
		if _, err := fs.Stat(sub, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Otherwise serve index.html (SPA fallback)
		data, _ := fs.ReadFile(sub, "index.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
```

**go.mod:**

```
module hello

go 1.26
```

## React Frontend — Relative API Calls

The React app calls relative URLs (`/api/...`). Same origin, no CORS headers needed.

**`vite.config.ts`** — in dev mode, proxy `/api` to the Go backend:

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
  },
})
```

**`src/App.tsx`** — fetch with relative paths:

```tsx
import { useEffect, useState } from 'react'

function App() {
  const [msg, setMsg] = useState('')

  useEffect(() => {
    // Relative path — same origin, no CORS
    fetch('/api/hello')
      .then(res => res.json())
      .then(data => setMsg(data.message))
  }, [])

  const handleSubmit = async () => {
    // Also relative
    const res = await fetch('/api/data', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ foo: 'bar' }),
    })
    const data = await res.json()
    console.log(data)
  }

  return (
    <div>
      <h1>{msg || 'Loading...'}</h1>
      <button onClick={handleSubmit}>Send POST</button>
    </div>
  )
}

export default App
```

## Running It

### Development (two processes, proxied)

```sh
# Terminal 1: Go backend
cd backend && go run .

# Terminal 2: Vite dev server (proxies /api to Go)
cd frontend && npm run dev
```

Open `http://localhost:3000` — Vite dev server proxies `/api/*` to the Go backend. No CORS.

### Production (single binary, embedded React)

```sh
# Build React
cd frontend && npm run build

# Copy dist into Go module so //go:embed picks it up
cp -r dist ../backend/

# Build the single binary
cd ../backend && go build -o app .

# Run (everything on one port)
./app
```

Now `http://localhost:8080` serves both the React UI and the `/api/*` endpoints from one binary.

## How CORS Is Avoided

| Scenario | Request → | Origin | Same? |
|---|---|---|---|
| Production `localhost:8080` | `/api/hello` → `same host:8080` | `localhost:8080` | ✅ Same origin |
| Dev `localhost:3000` | `/api/hello` → Vite proxy → `localhost:8080` | Browser sees `localhost:3000` | ✅ No cross-origin request |

The rule: **same scheme + same host + same port = same origin = no CORS needed.**

## Alternative: Go Only Serves API, React Is Separate

If you prefer to keep React and Go as separate deployments but avoid CORS in development, you can use the Vite proxy (shown above). But in that scenario:

- **Production** you'd still need a reverse proxy (nginx, Caddy, or a Go API gateway) to serve both on one port
- Or you configure the Go backend to emit CORS headers (`Access-Control-Allow-Origin: *`)

The **embedded approach** (first section) is the simplest — one Go binary, one port, zero CORS config anywhere.

---

## Nginx Reverse Proxy (Alternative)

Instead of embedding the React build in Go, you can use nginx as a reverse proxy. This is useful when you want separation of concerns, or when both apps already exist independently and you want to stitch them together without touching code.

### How It Works

```
Browser → nginx (port 80/443)
              ├── /api/* → proxy_pass to Go backend (e.g. localhost:8080)
              └── /*      → serves React static files from disk
```

Because nginx serves both on the **same origin**, the browser never makes cross-origin requests — no CORS needed.

### nginx Config

```nginx
server {
    listen 80;
    server_name example.com;

    # React build output directory
    root /var/www/react-app/dist;
    index index.html;

    # API requests — proxy to Go backend
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # SPA fallback — any non-file request returns index.html
    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

### Go Backend (API only)

The Go binary no longer needs `//go:embed` — just the API routes:

```go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Hello from Go!",
		})
	})

	mux.HandleFunc("POST /api/data", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"received": body,
			"status":   "ok",
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
```

### React Frontend

Same as before — uses relative `/api/...` paths. The Vite dev proxy still works for local development.

**vite.config.ts:**

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
  },
})
```

### Running in Production

```sh
# Build React
cd frontend && npm run build

# Deploy the dist/ directory — nginx serves it from /var/www/react-app/dist

# Build and run the Go API
cd backend && go build -o api .
./api &

# Start nginx
sudo systemctl restart nginx   # or sudo nginx -s reload
```

Now `http://example.com` serves everything through nginx on port 80 — no CORS.

### Adding TLS (HTTPS) with Certbot

```nginx
server {
    listen 443 ssl;
    server_name example.com;

    ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;

    root /var/www/react-app/dist;
    index index.html;

    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location / {
        try_files $uri $uri/ /index.html;
    }
}

# Redirect HTTP to HTTPS
server {
    listen 80;
    server_name example.com;
    return 301 https://$host$request_uri;
}
```

### Nginx vs Embedded: When to Use Which

| Approach | Pros | Cons |
|---|---|---|
| **Embedded** (`//go:embed`) | Single binary deployment; no reverse proxy to manage | Go binary re-build needed when React changes |
| **Nginx** | React and Go deploy independently; TLS termination; caching/rate-limiting at the proxy | Extra service to manage (nginx) |

Both achieve the same goal: **same-origin requests, no CORS needed.**
