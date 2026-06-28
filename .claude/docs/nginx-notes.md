# NGINX — Explained

**NGINX** (pronounced "engine-x") is an open-source web server that also functions as a **reverse proxy**, **load balancer**, **mail proxy**, and **HTTP cache**. Created by Igor Sysoev in 2004, it was designed to solve the **C10k problem** — handling 10,000+ concurrent connections efficiently.

## Architecture: Why It's Fast

Unlike traditional web servers (like Apache) that use a **thread-per-connection** or **process-per-connection** model, NGINX uses an **event-driven, asynchronous, non-blocking** architecture:

```
Apache (process-based):
  Request → Fork/Thread → Block until done → Return
  Problem: 10,000 connections = 10,000 threads = huge memory

NGINX (event-driven):
  Request → Event loop → Non-blocking I/O → Return
  Result: 10,000 connections handled by a handful of worker processes
```

This means NGINX can serve **tens of thousands of concurrent connections** with very low memory usage — typically a few megabytes per worker.

## Core Components

### 1. Master Process and Worker Processes
- **Master process** — reads config, binds ports, manages worker lifecycle
- **Worker processes** — handle actual requests (one per CPU core is typical)
- Workers are single-threaded event loops; they never block

### 2. Configuration Structure
```nginx
# /etc/nginx/nginx.conf

worker_processes auto;        # One worker per CPU core
events {
    worker_connections 1024;  # Max simultaneous connections per worker
}

http {
    server {
        listen 80;
        server_name example.com;

        location / {
            root /var/www/html;
            index index.html;
        }
    }
}
```

## Key Use Cases

### 1. Static File Serving
Serve HTML, CSS, JS, images directly from disk — extremely fast with `sendfile` and AIO support.

### 2. Reverse Proxy
Forward requests to backend apps (Node.js, Python, Go, etc.):
```nginx
location /api/ {
    proxy_pass http://localhost:3000/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
}
```

#### Header-Based Routing

NGINX can route traffic based on any HTTP header value by referencing it as `$http_<name>` (lowercased, dashes → underscores). `X-Service-Name` becomes `$http_x_service_name`.

The `map` directive is the cleanest approach — it maps header values to backends with a `default` fallback:

```nginx
map $http_x_service_name $backend {
    default          http://default:3000;
    auth             http://auth:3001;
    payments         http://payments:3002;
    notifications    http://notifications:3003;
}

server {
    listen 80;

    location / {
        proxy_pass $backend;
    }
}
```

A request with `X-Service-Name: payments` routes to `http://payments:3002`; any unrecognized value falls through to `default`. The `map` block lives in the `http` context (outside `server`).

Inline routing with `if` is possible but `map` is preferred — it avoids `if` quirks inside `location` blocks:

```nginx
location / {
    if ($http_x_service_name = "auth") {
        proxy_pass http://auth:3001;
    }
}
```

**Header variable naming rule:** every incoming header is exposed as `$http_<name>` — name is lowercased, hyphens become underscores. Examples: `Authorization` → `$http_authorization`, `X-Request-ID` → `$http_x_request_id`, `Content-Type` → `$http_content_type`.

#### Multi-Header Routing

Route on multiple headers by concatenating them into a composite key inside `map`:

```nginx
map $http_x_service_name$http_x_environment $backend {
    # auth service × environment
    authdev             http://auth-dev:3001;
    authstaging         http://auth-staging:3001;
    authprod            http://auth-prod:3001;
    # payments service × environment
    paymentsdev         http://payments-dev:3002;
    paymentsstaging     http://payments-staging:3002;
    paymentsprod        http://payments-prod:3002;

    default             http://default:3000;
}

server {
    listen 80;

    location / {
        proxy_pass $backend;
    }
}
```

A request with `X-Service-Name: auth` + `X-Environment: staging` matches `authstaging` → `http://auth-staging:3001`.

**Alternative — two-stage `map`** (scales better when there are many services × environments):

```nginx
# Stage 1: pick the service
map $http_x_service_name $service_backend {
    default          http://default;
    auth             http://auth;
    payments         http://payments;
}

# Stage 2: append the environment subdomain
map $http_x_environment $env_suffix {
    default          ;
    dev              -dev;
    staging          -staging;
    prod             ;                        # production has no suffix
}

server {
    listen 80;

    location / {
        set $target "$service_backend$env_suffix:3000";
        proxy_pass $target;
    }
}
```

Result: `auth` + `staging` → `http://auth-staging:3000`, `payments` + `prod` → `http://payments:3000`. Adding a new service or environment is just one more line in the corresponding `map`.

**Alternative — chained `if` with a variable** (workable for simple cases, but `map` is safer):

NGINX's `if` does not support `AND` and `proxy_pass` inside `if` has URI-handling quirks. The workaround: chain `if` blocks that manipulate a variable, then `proxy_pass` the variable outside all `if` blocks:

```nginx
server {
    listen 80;

    location / {
        set $target "http://default:3000";

        if ($http_x_service_name = "auth") {
            set $target "http://auth";
        }
        if ($http_x_environment = "staging") {
            set $target "${target}-staging";
        }
        if ($http_x_environment = "dev") {
            set $target "${target}-dev";
        }

        set $target "$target:3000";
        proxy_pass $target;
    }
}
```

Each `if` matches independently and chains its `set` — but there's no way to express `AND` (e.g., "auth AND staging"). For multi-header routing, the composite-key `map` or two-stage `map` is preferred: no `if` quirks, easier to reason about as the matrix of values grows.

### 3. Load Balancing
Distribute traffic across multiple backends:
```nginx
upstream backend {
    server 10.0.0.1:3000 weight=3;   # 3× more traffic
    server 10.0.0.2:3000;            # default weight=1
    server 10.0.0.3:3000 backup;     # only used if others are down
}

server {
    location / {
        proxy_pass http://backend;
    }
}
```
Algorithms: round-robin (default), least_conn, ip_hash, random, least_time (NGINX Plus).

### 4. TLS/SSL Termination
Handle HTTPS at the proxy layer so backends only deal with plain HTTP:
```nginx
server {
    listen 443 ssl;
    ssl_certificate     /etc/nginx/certs/example.com.crt;
    ssl_certificate_key /etc/nginx/certs/example.com.key;
}
```

### 5. HTTP Caching
Cache backend responses to reduce load:
```nginx
proxy_cache_path /var/cache/nginx levels=1:2 keys_zone=my_cache:10m;
location / {
    proxy_cache my_cache;
    proxy_cache_valid 200 1h;  # Cache 200 responses for 1 hour
}
```

### 6. Rate Limiting
Protect backends from abuse:
```nginx
limit_req_zone $binary_remote_addr zone=mylimit:10m rate=10r/s;
location /api/ {
    limit_req zone=mylimit burst=20;
}
```

### 7. gzip/Brotli Compression and HTTP/2, gRPC Proxying

## NGINX vs. Apache

| | NGINX | Apache |
|---|---|---|
| **Model** | Event-driven, non-blocking | Process/thread per connection |
| **Concurrency** | Excellent (100K+ connections) | Good (depends on thread limit) |
| **Memory per conn** | ~2.5 KB | ~1-2 MB |
| **Dynamic config** | Limited (.htaccess not supported) | Per-directory .htaccess |
| **Modules** | Compiled-in at build time | Dynamically loaded |
| **Static files** | Faster | Good |

## NGINX Plus (Commercial)

NGINX Plus adds enterprise features: active health checks, session persistence (sticky sessions), live dashboard, dynamic reconfiguration via API, and JWT/OpenID Connect auth. The open-source version covers ~90% of use cases.

## Common Deployment Pattern

```
Internet → NGINX (TLS, static files, rate limiting)
              ↓
         /api/* → Backend App Server (port 3000)
              ↓
         /static/* → Disk
```

**In short:** NGINX is the de facto entry point for web traffic — it handles TLS, routing, caching, and rate limiting so your application servers only deal with business logic.
