# mTLS for Microservice Security

**mTLS (Mutual TLS)** extends standard TLS by requiring both the client *and* the server to present certificates and prove their identity. In a microservice architecture, this means every service-to-service call is mutually authenticated.

---

## The Problem It Solves

Standard TLS only verifies the server's identity. In a mesh of microservices, you need to answer:

- Is the *caller* who they claim to be?
- Is the callee the legitimate service, not an imposter?
- Is the traffic encrypted end-to-end between every hop?

mTLS answers all three.

---

## How the Handshake Works

```
Client (Service A)                          Server (Service B)
     |                                            |
     |  1. ClientHello  ---------------------->   |
     |                                            |
     |  2. <---------------------  ServerHello    |
     |      ServerCertificate                     |
     |      CertificateRequest  ← (the "mutual")  |
     |                                            |
     |  3. ClientCertificate  ---------------->   |
     |      ClientCertificateVerify               |
     |      (proves possession of private key)    |
     |                                            |
     |  4. <--- Finished, session keys exchanged  |
     |      Finished  ------------------------>   |
     |                                            |
     |  5. Encrypted application data flows       |
```

The key addition: **step 2 includes a `CertificateRequest`**, and step 3 has the client proving it owns the private key corresponding to its certificate.

---

## PKI Architecture for Microservices

In a service mesh, you typically need a **Private CA (Certificate Authority)**:

```
                    ┌──────────────┐
                    │  Root CA     │  (offline, air-gapped)
                    │ (self-signed)│
                    └──────┬───────┘
                           │ signs
                    ┌──────▼───────┐
                    │  Intermediate│  (online, issues leaf certs)
                    │  CA          │
                    └──────┬───────┘
           ┌───────────────┼───────────────┐
           │ signs         │ signs         │ signs
    ┌──────▼──────┐ ┌──────▼──────┐ ┌──────▼──────┐
    │ Service A   │ │ Service B   │ │ Service C   │
    │ cert + key  │ │ cert + key  │ │ cert + key  │
    └─────────────┘ └─────────────┘ └─────────────┘
```

### Why Two Tiers? Root vs. Intermediate CA

The separation isn't bureaucracy — it's a **security boundary**. The root CA's private key is the keys to the entire mesh: if stolen, an attacker can mint valid certificates for any service, and every service that trusts the root CA will accept them. Revoking that trust means rotating the CA bundle on every single pod — a rebuild-the-world event.

The two tiers isolate that risk:

| Tier | Where | Issues | Lifetime | Online? |
|------|-------|--------|----------|---------|
| **Root CA** | Offline HSM / air-gapped machine / physical safe | 1 intermediate CA cert | 5–10 years | **No** |
| **Intermediate CA** | `cert-manager` cluster issuer | Thousands of leaf certs (every new pod, every renewal) | Months to 1 year | **Yes** |

**The root CA stays offline.** It signs exactly one thing — the intermediate CA certificate — and that happens once every few years. After that, its private key goes into an HSM or a sealed envelope in a safe. It never touches a network, so compromising it requires physical access.

**The intermediate CA is online.** It issues thousands of leaf certificates per day and is exposed to whatever risk comes with being network-connected. If it's breached, you revoke the intermediate, sign a new one with the offline root, and rotate the intermediate certs. The root key remains safe — services don't need a new CA bundle because the root in their trust store hasn't changed.

**Blast radius comparison:**

```
Root CA compromised →    Every service in the mesh must get a new CA bundle.
                         You're rebuilding trust from zero.

Intermediate CA →        Root revokes the intermediate (via CRL).
compromised               Issue a new intermediate from the same root.
                         Leaf cert rotation is already automated.
                         Services don't need a new CA bundle —
                         the root in their trust store hasn't changed.
```

The difference is between "rotate some intermediate certs" and "re-deploy the CA trust bundle to every pod in every cluster."

**Verifiers only need the root.** During the mTLS handshake, the peer sends the full certificate chain (leaf → intermediate → root). The verifier walks the chain until it hits a certificate it already trusts — the root CA in its `ca.crt`. The intermediate is just a link in that chain; the verifier doesn't need it pre-configured. This is why only the root sits in the trust store: one file to distribute, one thing to trust.

### Certificate Properties

Each service gets a **short-lived leaf certificate** (often 24h or less) with:

| Field | Example | Purpose |
|-------|---------|---------|
| **CN** | `service-a` | Service identity |
| **SAN (URI)** | `spiffe://cluster.local/ns/default/sa/service-a` | SPIFFE identity (mesh standard) |
| **SAN (DNS)** | `service-a.ns.svc.cluster.local` | Kubernetes DNS name |
| **TTL** | `1h` – `24h` | Limits blast radius of key compromise |
| **Key Usage** | `digitalSignature`, `keyEncipherment` | Restricts what the key can do |
| **Extended Key Usage** | `clientAuth`, `serverAuth` | Explicitly marks it as usable for both roles |

### Understanding SAN URI

A **SAN URI** is a Subject Alternative Name entry in an X.509 certificate that uses a URI scheme instead of a DNS name or IP address.

The SAN extension can contain several types of identifiers:

| SAN Type | Example | Used For |
|----------|---------|----------|
| **dNSName** | `api.example.com` | Traditional hostname validation |
| **iPAddress** | `10.0.1.5` | IP-based services |
| **rfc822Name** | `admin@example.com` | Email certificates (S/MIME) |
| **uniformResourceIdentifier** | `spiffe://cluster.local/ns/prod/sa/payments` | Workload identity (SPIFFE) |

#### Why URI Instead of DNS?

DNS names answer "where is this service reachable?" A URI answers "**who** is this workload?" — a fundamentally different question.

```
dNSName:    payments.prod.svc.cluster.local   → "I'm at this address"
URI:        spiffe://cluster.local/ns/prod/sa/payments  → "I am the payments service in prod"
```

DNS names change when you redeploy or move between clusters. The SPIFFE URI stays stable because it encodes **logical identity** — namespace, service account — not network topology.

#### SPIFFE: The De Facto URI Scheme

[SPIFFE](https://spiffe.io/) (Secure Production Identity Framework for Everyone) defines the `spiffe://` URI scheme as the standard for workload identity in cloud-native environments. The format:

```
spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>
```

| Part | Meaning |
|------|---------|
| `spiffe://` | Scheme — signals this is a SPIFFE identity |
| `cluster.local` | Trust domain — scopes the identity to a specific cluster or org |
| `/ns/prod` | Kubernetes namespace |
| `/sa/payments` | Kubernetes service account name |

#### How It's Validated

During mTLS, the server doesn't just check that the client cert is signed by the trusted CA — it also verifies the **SAN URI** matches the expected identity:

```go
// Go TLS: verify a specific SPIFFE ID after the handshake
func verifySPIFFEID(conn *tls.Conn, expectedID string) error {
    certs := conn.ConnectionState().PeerCertificates
    if len(certs) == 0 {
        return fmt.Errorf("no peer certificate")
    }
    for _, uri := range certs[0].URIs {
        if uri.String() == expectedID {
            return nil
        }
    }
    return fmt.Errorf("SPIFFE ID mismatch: expected %s, got %v", expectedID, certs[0].URIs)
}
```

Istio and Linkerd do this automatically — the sidecar extracts the SPIFFE URI from the peer certificate and makes it available as the `source.principal` in authorization policies.

---

## Complete Example: Customer Management mTLS

Here we'll build the full CA hierarchy and mTLS configuration for a **Customer Management System** with a single API — `/limits` — called by three clients. Each client certificate carries a distinct identity, and the server enforces that only recognized clients can connect.

**Architecture:**

```
┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐
│   risk-engine   │   │  order-service  │   │ billing-service │
│  (client cert)  │   │  (client cert)  │   │  (client cert)  │
└────────┬────────┘   └────────┬────────┘   └────────┬────────┘
         │                     │                     │
         └─────────────────────┼─────────────────────┘
                               │ mTLS (mutual auth)
                      ┌────────▼────────┐
                      │ customer-limits │
                      │      API        │
                      │  (server cert)  │
                      └─────────────────┘
```

All certs are signed by the same intermediate CA, which chains to a shared root CA. Each client's identity is encoded as a **SAN URI** in its leaf certificate.

### Identity Scheme

| Entity | SAN DNS | SAN URI (identity) |
|--------|---------|-------------------|
| Server (API) | `customer-limits-api.customers.internal` | `urn:customers:api:limits` |
| Client 1 | `risk-engine.customers.internal` | `urn:customers:client:risk-engine` |
| Client 2 | `order-service.customers.internal` | `urn:customers:client:order-service` |
| Client 3 | `billing-service.customers.internal` | `urn:customers:client:billing-service` |

DNS SANs answer "where to reach me." URI SANs answer "**who I am**" — the server checks the URI, not the DNS name, to authorize clients.

### Step 1: Root CA (Offline, Self-Signed)

Create a config file for the root CA:

```ini
# root-ca.cnf
[req]
default_bits        = 4096
prompt              = no
default_md          = sha256
distinguished_name  = dn
x509_extensions     = v3_ca

[dn]
C  = US
O  = Customer Management System
CN = Root CA

[v3_ca]
basicConstraints    = critical, CA:TRUE
keyUsage            = critical, keyCertSign, cRLSign
subjectKeyIdentifier = hash
```

Generate the root CA key and self-signed certificate:

```bash
# Generate the root CA (self-signed, 10-year validity)
openssl req -x509 -new -nodes \
  -keyout certs/root-ca.key \
  -out certs/root-ca.crt \
  -config certs/root-ca.cnf \
  -days 3650

# Restrictive permissions — this key goes offline
chmod 400 certs/root-ca.key

# Inspect what we created
openssl x509 -in certs/root-ca.crt -text -noout | head -20
```

### Step 2: Intermediate CA (Signed by Root)

```ini
# int-ca.cnf
[req]
default_bits        = 4096
prompt              = no
default_md          = sha256
distinguished_name  = dn

[dn]
C  = US
O  = Customer Management System
CN = Intermediate CA

[v3_intermediate]
basicConstraints    = critical, CA:TRUE, pathlen:0
keyUsage            = critical, keyCertSign, cRLSign
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always
```

The `pathlen:0` constraint means this intermediate can only sign leaf (end-entity) certificates — it cannot create further intermediate CAs. This limits the blast radius if the intermediate key is compromised.

```bash
# Generate key + CSR for the intermediate CA
openssl req -new -nodes \
  -keyout certs/int-ca.key \
  -out certs/int-ca.csr \
  -config certs/int-ca.cnf

# Sign the intermediate CA with the root CA
openssl x509 -req \
  -in certs/int-ca.csr \
  -out certs/int-ca.crt \
  -CA certs/root-ca.crt \
  -CAkey certs/root-ca.key \
  -CAcreateserial \
  -days 1825 \
  -extfile certs/int-ca.cnf \
  -extensions v3_intermediate

# Verify the chain: root → intermediate
openssl verify -CAfile certs/root-ca.crt certs/int-ca.crt
# Expected: int-ca.crt: OK

# Create the trust bundle (root only — distributed to all services)
cp certs/root-ca.crt certs/ca-bundle.crt
```

### Step 3: Server Certificate (Customer Limits API)

```ini
# server.cnf
[req]
default_bits        = 2048
prompt              = no
default_md          = sha256
distinguished_name  = dn
req_extensions      = v3_req

[dn]
C  = US
O  = Customer Management System
CN = customer-limits-api

[v3_req]
basicConstraints    = critical, CA:FALSE
keyUsage            = critical, digitalSignature, keyEncipherment
extendedKeyUsage    = serverAuth
subjectAltName      = @alt_names

[alt_names]
DNS.1 = customer-limits-api.customers.internal
URI.1 = urn:customers:api:limits
```

The `extendedKeyUsage = serverAuth` restricts this certificate to server-side TLS only — it cannot be used as a client certificate.

```bash
# Generate key + CSR for the server
openssl req -new -nodes \
  -keyout certs/server.key \
  -out certs/server.csr \
  -config certs/server.cnf

# Sign the server cert with the intermediate CA
openssl x509 -req \
  -in certs/server.csr \
  -out certs/server.crt \
  -CA certs/int-ca.crt \
  -CAkey certs/int-ca.key \
  -CAcreateserial \
  -days 365 \
  -extfile certs/server.cnf \
  -extensions v3_req

# Bundle server cert + intermediate for the TLS handshake
# (the server sends the full chain so clients can verify up to the root)
cat certs/server.crt certs/int-ca.crt > certs/server-chain.crt

# Verify: root → intermediate → server
openssl verify -CAfile certs/ca-bundle.crt -untrusted certs/int-ca.crt certs/server.crt
# Expected: server.crt: OK
```

### Step 4: Client Certificates (One Per Caller)

Each client gets a certificate whose **URI SAN uniquely identifies it**. This is what the server checks to distinguish `risk-engine` from `order-service`.

**Config for risk-engine** (repeat for each client, changing the identity fields):

```ini
# client-risk-engine.cnf
[req]
default_bits        = 2048
prompt              = no
default_md          = sha256
distinguished_name  = dn
req_extensions      = v3_req

[dn]
C  = US
O  = Customer Management System
CN = risk-engine

[v3_req]
basicConstraints    = critical, CA:FALSE
keyUsage            = critical, digitalSignature
extendedKeyUsage    = clientAuth
subjectAltName      = @alt_names

[alt_names]
DNS.1 = risk-engine.customers.internal
URI.1 = urn:customers:client:risk-engine
```

The `extendedKeyUsage = clientAuth` restricts this certificate to client-side TLS only — it cannot be used as a server certificate.

```bash
# --- risk-engine ---
openssl req -new -nodes \
  -keyout certs/client-risk-engine.key \
  -out certs/client-risk-engine.csr \
  -config certs/client-risk-engine.cnf

openssl x509 -req \
  -in certs/client-risk-engine.csr \
  -out certs/client-risk-engine.crt \
  -CA certs/int-ca.crt \
  -CAkey certs/int-ca.key \
  -CAcreateserial \
  -days 365 \
  -extfile certs/client-risk-engine.cnf \
  -extensions v3_req

# --- order-service ---
openssl req -new -nodes \
  -keyout certs/client-order-service.key \
  -out certs/client-order-service.csr \
  -config certs/client-order-service.cnf

openssl x509 -req \
  -in certs/client-order-service.csr \
  -out certs/client-order-service.crt \
  -CA certs/int-ca.crt \
  -CAkey certs/int-ca.key \
  -CAcreateserial \
  -days 365 \
  -extfile certs/client-order-service.cnf \
  -extensions v3_req

# --- billing-service ---
openssl req -new -nodes \
  -keyout certs/client-billing-service.key \
  -out certs/client-billing-service.csr \
  -config certs/client-billing-service.cnf

openssl x509 -req \
  -in certs/client-billing-service.csr \
  -out certs/client-billing-service.crt \
  -CA certs/int-ca.crt \
  -CAkey certs/int-ca.key \
  -CAcreateserial \
  -days 365 \
  -extfile certs/client-billing-service.cnf \
  -extensions v3_req
```

### Step 5: Verify the Chains

```bash
# Each client cert should chain back to the root
for client in risk-engine order-service billing-service; do
  echo "=== $client ==="
  openssl verify -CAfile certs/ca-bundle.crt -untrusted certs/int-ca.crt certs/client-${client}.crt
done

# Each should output: client-<name>.crt: OK

# Confirm the URI SAN is embedded correctly
openssl x509 -in certs/client-risk-engine.crt -text -noout | grep -A1 "Subject Alternative"
# Expected: URI:urn:customers:client:risk-engine
```

### Step 6: Go Server — Enforce Client Identity

The server loads its certificate, trusts only the root CA, requires client certificates, and uses `VerifyPeerCertificate` to check that the client's **URI SAN** matches an allowed identity:

```go
// server/main.go — Customer Limits API with per-client identity verification
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	// Load server certificate chain (server + intermediate → full chain to send clients)
	cert, err := tls.LoadX509KeyPair("certs/server-chain.crt", "certs/server.key")
	if err != nil {
		log.Fatalf("failed to load server cert: %v", err)
	}

	// Trust store: root CA only
	caPool := x509.NewCertPool()
	caPEM, err := os.ReadFile("certs/ca-bundle.crt")
	if err != nil {
		log.Fatalf("failed to read CA bundle: %v", err)
	}
	caPool.AppendCertsFromPEM(caPEM)

	// Recognized client identities: URI SAN → display name
	allowedClients := map[string]string{
		"urn:customers:client:risk-engine":    "Risk Engine",
		"urn:customers:client:order-service":  "Order Service",
		"urn:customers:client:billing-service": "Billing Service",
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert, // ← mTLS: demand client cert
		ClientCAs:    caPool,                         // only trust certs signed by our root
		MinVersion:   tls.VersionTLS13,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			// rawCerts[0] is the client's leaf certificate.
			// The chain has already been verified against ClientCAs at this point —
			// we do additional identity checks on the leaf.
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("failed to parse client certificate: %w", err)
			}

			// Check URI SAN for a recognized client identity
			for _, uri := range leaf.URIs {
				if name, ok := allowedClients[uri.String()]; ok {
					log.Printf("mTLS handshake: accepted %s (%s)", name, uri)
					return nil // identity confirmed
				}
			}
			return fmt.Errorf("unrecognized client — URIs: %v, DNS: %v", leaf.URIs, leaf.DNSNames)
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/limits", func(w http.ResponseWriter, r *http.Request) {
		// Extract client identity from the verified TLS handshake state
		clientCert := r.TLS.PeerCertificates[0]
		var clientID string
		for _, uri := range clientCert.URIs {
			clientID = uri.String()
			break
		}
		name := allowedClients[clientID]

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"client":   name,
			"limit":    50000,
			"currency": "USD",
		})
		log.Printf("GET /limits → client=%s (%s)", name, clientID)
	})

	srv := &http.Server{Addr: ":8443", TLSConfig: tlsConfig, Handler: mux}
	log.Println("Customer Limits API listening on :8443 (mTLS required)")
	log.Fatal(srv.ListenAndServeTLS("", ""))
}
```

Key points:
- `ClientAuth: tls.RequireAndVerifyClientCert` — the server **rejects** connections without a valid client certificate. No plaintext fallback.
- `ClientCAs` contains **only** the root CA — the intermediate is never placed in the trust store.
- `VerifyPeerCertificate` runs **after** the standard chain verification — Go already validated the chain reaches the root CA. This callback adds application-level identity checks (URI SAN match).
- The intermediate CA is sent as part of the server's certificate chain (`server-chain.crt`) so clients can verify up to the root they trust.

### Step 7: Go Client — Present Identity

Each client loads its own certificate and key, trusts the root CA, and presents its identity during the mTLS handshake:

```go
// client/main.go — mTLS client for Customer Limits API
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: client <risk-engine|order-service|billing-service>")
	}
	clientName := os.Args[1]

	// Load this client's certificate and key
	cert, err := tls.LoadX509KeyPair(
		fmt.Sprintf("certs/client-%s.crt", clientName),
		fmt.Sprintf("certs/client-%s.key", clientName),
	)
	if err != nil {
		log.Fatalf("failed to load client cert for %s: %v", clientName, err)
	}

	// Trust store: root CA only
	caPool := x509.NewCertPool()
	caPEM, err := os.ReadFile("certs/ca-bundle.crt")
	if err != nil {
		log.Fatalf("failed to read CA bundle: %v", err)
	}
	caPool.AppendCertsFromPEM(caPEM)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert}, // ← presents this client's identity
				RootCAs:      caPool,                  // verifies server is signed by our root
				MinVersion:   tls.VersionTLS13,
				// ServerName must match the server cert's DNS SAN
				ServerName: "customer-limits-api.customers.internal",
			},
		},
	}

	resp, err := client.Get("https://customer-limits-api.customers.internal:8443/limits")
	if err != nil {
		log.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
}
```

Go automatically validates the server's DNS SAN against `ServerName` — no `VerifyPeerCertificate` needed on the client side for standard server verification.

### Step 8: Test the Connection

**With openssl s_client** (quick certificate-level test):

```bash
# Start the server in one terminal, then test as risk-engine:
openssl s_client -connect localhost:8443 \
  -cert certs/client-risk-engine.crt \
  -key certs/client-risk-engine.key \
  -CAfile certs/ca-bundle.crt \
  -verify_return_error <<< "GET /limits HTTP/1.1\r\nHost: customer-limits-api.customers.internal\r\n\r\n"

# Expected output includes the JSON response and:
# Verify return code: 0 (ok)

# Try without a client cert — should fail:
openssl s_client -connect localhost:8443 \
  -CAfile certs/ca-bundle.crt
# Expected: alert certificate required (server rejects the connection)
```

**With curl** (HTTP-level test):

```bash
curl --cert certs/client-risk-engine.crt \
     --key certs/client-risk-engine.key \
     --cacert certs/ca-bundle.crt \
     --resolve customer-limits-api.customers.internal:8443:127.0.0.1 \
     https://customer-limits-api.customers.internal:8443/limits

# Response: {"client":"Risk Engine","currency":"USD","limit":50000}
```

**Running the Go client:**

```bash
# Each client gets the same API response, authenticated by its own identity
go run client/main.go risk-engine
# → {"client":"Risk Engine","currency":"USD","limit":50000}

go run client/main.go order-service
# → {"client":"Order Service","currency":"USD","limit":50000}

go run client/main.go billing-service
# → {"client":"Billing Service","currency":"USD","limit":50000}

# An unknown client would fail at the TLS handshake level —
# VerifyPeerCertificate rejects it before any HTTP handler runs.
```

### What Happens When Something Goes Wrong

| Scenario | What breaks | Error |
|----------|------------|-------|
| Client presents a cert from an untrusted CA | Go's standard chain verification | `tls: unknown certificate authority` |
| Client presents no certificate | `ClientAuth: RequireAndVerifyClientCert` | `tls: certificate required` |
| Client cert is valid but URI SAN is unknown | `VerifyPeerCertificate` callback | `unrecognized client — URIs: [...], DNS: [...]` |
| Client cert's EKU is `serverAuth`, not `clientAuth` | Go's EKU check during chain verification | `tls: client certificate's Extended Key Usage doesn't permit client authentication` |
| Client trusts the system CA pool, not the private root | Client-side verification | `tls: failed to verify certificate: x509: certificate signed by unknown authority` |
| Server cert's DNS SAN doesn't match `ServerName` | Go's automatic DNS SAN check | `tls: failed to verify certificate: x509: certificate is valid for ..., not ...` |

Each failure mode is caught at the TLS layer — the HTTP handler code never runs for an unauthenticated caller.

---

## Where It Runs in the Stack

Three common patterns:

### 1. Service Mesh Sidecar (Istio/Linkerd/Consul Connect)

```
┌──────────────────────┐
│  Service A (app)     │  ← plain HTTP to localhost
│  localhost:8080      │
└──────────┬───────────┘
           │ (plaintext, loopback)
┌──────────▼───────────┐
│  Envoy sidecar       │  ← mTLS to peer sidecar
│  (mTLS termination)  │
└──────────┬───────────┘
           │ (encrypted, authenticated)
           ▼
   [ network ]
           │
┌──────────▼───────────┐
│  Envoy sidecar       │
│  (mTLS termination)  │
└──────────┬───────────┘
           │ (plaintext, loopback)
┌──────────▼───────────┐
│  Service B (app)     │
└──────────────────────┘
```

- **Pro**: Zero code changes. The sidecar handles everything.
- **Con**: Operational complexity of running a sidecar per pod.

### 2. Application-Level mTLS

The application itself loads a cert and key, configures its HTTP/TCP library for mTLS. In Go:

```go
// Load service cert and CA pool
cert, _ := tls.LoadX509KeyPair("/etc/certs/tls.crt", "/etc/certs/tls.key")
caPool := x509.NewCertPool()
caPem, _ := os.ReadFile("/etc/certs/ca.crt")
caPool.AppendCertsFromPEM(caPem)

// Server: requires client certs
serverTLS := &tls.Config{
    Certificates: []tls.Certificate{cert},
    ClientAuth:   tls.RequireAndVerifyClientCert,
    ClientCAs:    caPool,
    MinVersion:   tls.VersionTLS13,
}

// Client: presents its own cert
clientTLS := &tls.Config{
    Certificates: []tls.Certificate{cert},
    RootCAs:      caPool,
    MinVersion:   tls.VersionTLS13,
}
```

- **Pro**: Fine-grained control, no sidecar overhead.
- **Con**: Every service must implement it; language-specific.

#### Go TLS SAN Validation: Asymmetric by Default

Go's `crypto/tls` validates SANs differently depending on which side of the handshake you're on:

**Client-side (dialing a server) — automatic:**

When you dial, Go verifies the server's certificate SANs against `tls.Config.ServerName` with no extra code:

```go
// Go validates this automatically:
conn, err := tls.Dial("tcp", "payments.prod.svc.cluster.local:443", &tls.Config{
    ServerName: "payments.prod.svc.cluster.local", // ← matched against server's SAN dNSName
    RootCAs:    caPool,
})
```

What Go checks automatically:
1. Extracts `dNSName` entries from the server cert's SAN extension.
2. Matches `ServerName` against them (with wildcard support, e.g., `*.cluster.local`).
3. If no SAN DNS names exist, falls back to the **Common Name** (CN) — deprecated per RFC 2818, but still supported by Go.
4. Fails the handshake (`x509: certificate is not valid for any names…`) if nothing matches.

**Server-side (receiving client cert in mTLS) — you must verify:**

Setting `ClientAuth: tls.RequireAndVerifyClientCert` only:
- Verifies the cert chain is signed by a trusted CA (`ClientCAs`).
- Checks the cert is valid for `clientAuth` extended key usage.

It does **not** check that a particular DNS name or URI is present. You must verify that yourself via `VerifyPeerCertificate`:

```go
serverTLS := &tls.Config{
    ClientAuth: tls.RequireAndVerifyClientCert,
    ClientCAs:  caPool,
    VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
        cert, _ := x509.ParseCertificate(rawCerts[0])

        // DNS SAN — you verify it manually
        for _, name := range cert.DNSNames {
            if name == "allowed-client.ns.svc.cluster.local" {
                return nil
            }
        }
        return fmt.Errorf("unexpected client DNS identity: %v", cert.DNSNames)
    },
}
```

**Validating URI SAN (SPIFFE ID):**

Since Go never validates URI SAN automatically, you use the same `VerifyPeerCertificate` callback — this time checking the `URIs` field:

```go
// serverTLS validates SPIFFE URI from the client cert
serverTLS := &tls.Config{
    ClientAuth: tls.RequireAndVerifyClientCert,
    ClientCAs:  caPool,
    VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
        cert, _ := x509.ParseCertificate(rawCerts[0])

        // Validate SPIFFE URI SAN
        expectedSPIFFE := "spiffe://cluster.local/ns/prod/sa/orders-sa"
        for _, uri := range cert.URIs {
            if uri.String() == expectedSPIFFE {
                return nil // identity confirmed
            }
        }
        return fmt.Errorf("SPIFFE ID mismatch: expected %s, got %v", expectedSPIFFE, cert.URIs)
    },
}
```

The `cert.URIs` field is a `[]*url.URL` — each URI SAN entry from the certificate is already parsed into a `*url.URL`, so you call `.String()` to compare it against your expected SPIFFE ID.

You can combine both checks if your policy requires matching on multiple dimensions:

```go
VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
    cert, _ := x509.ParseCertificate(rawCerts[0])

    // Check SPIFFE URI
    expectedURI := "spiffe://cluster.local/ns/prod/sa/orders-sa"
    uriMatch := false
    for _, uri := range cert.URIs {
        if uri.String() == expectedURI {
            uriMatch = true
            break
        }
    }
    if !uriMatch {
        return fmt.Errorf("unexpected SPIFFE ID: %v", cert.URIs)
    }

    // Also check DNS SAN
    expectedDNS := "orders-sa.ns.svc.cluster.local"
    dnsMatch := false
    for _, name := range cert.DNSNames {
        if name == expectedDNS {
            dnsMatch = true
            break
        }
    }
    if !dnsMatch {
        return fmt.Errorf("unexpected DNS identity: %v", cert.DNSNames)
    }

    return nil
},
```

**Summary of Go's asymmetric behavior:**

| Direction | What Go validates automatically | What you must verify yourself |
|-----------|-------------------------------|-------------------------------|
| **Client → Server** | DNS SAN (and IP SAN) against `ServerName` | SPIFFE URI, any custom identity |
| **Server ← Client (mTLS)** | Nothing beyond CA signature + EKU | DNS SAN, SPIFFE URI — everything |

This is why in mTLS, both DNS SAN and URI SAN identity checks typically end up in `VerifyPeerCertificate` — Go gives you the raw peer cert and you decide which identity fields matter.

### 3. Ambient Mesh (Istio Ambient / Cilium)

mTLS moves to a per-node proxy (ztunnel) rather than per-pod sidecars:

```
┌──────────┐   ┌──────────┐
│  Pod A   │   │  Pod B   │
└────┬─────┘   └────┬─────┘
     │ (plain)      │ (plain)
┌────▼──────────────▼─────┐
│  ztunnel (per-node)     │  ← mTLS enforced here
└────────────┬────────────┘
             │ (encrypted to peer ztunnel)
```

- **Pro**: No sidecars. Lower resource overhead.
- **Con**: Newer, less battle-tested.

---

## Authorization on Top of mTLS

mTLS proves **identity**, not **authorization**. After authentication, you layer on authorization:

| Layer | What It Answers | Example |
|-------|----------------|---------|
| **mTLS** | "Who is calling?" | `spiffe://cluster/ns/prod/sa/payments` |
| **AuthZ policy** | "Are they allowed to call this endpoint?" | `payments-sa` can `POST /charges` |

Example Istio `AuthorizationPolicy`:

```yaml
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: payments-policy
spec:
  selector:
    matchLabels:
      app: payments
  rules:
  - from:
    - source:
        principals: ["cluster.local/ns/prod/sa/orders-sa"]
    to:
    - operation:
        methods: ["POST"]
        paths: ["/charges"]
```

This says: *only* the `orders-sa` service identity can POST to `/charges` on the payments service — even though mTLS would accept any valid certificate from the mesh CA.

---

## Certificate Rotation

Short-lived certs are essential. The flow:

1. Each service (or sidecar) has a **certificate agent** (e.g., `cert-manager`, Vault agent, SPIRE agent).
2. The agent authenticates to the CA using a stronger credential (e.g., a JWT from the Kubernetes service account, or a node attestation).
3. The CA issues a short-lived leaf cert (e.g., 1-hour TTL).
4. The agent writes the cert + key to a tmpfs volume mounted into the pod.
5. Before expiry, the agent renews. The filesystem is updated atomically.
6. The TLS library reloads — either by watching the file or via a hot-reload mechanism.

---

## Common Pitfalls

| Pitfall | Mitigation |
|---------|------------|
| **Long-lived certs** — losing a key means months of compromise | Issue certs with ≤24h TTL; automate rotation |
| **Wildcard certs** — one cert for `*.svc.cluster.local` breaks least-privilege | Per-service certs with specific SANs |
| **Trusting the wrong CA** — services configured with a public CA pool accept any public cert | Use a private CA pool, not the system pool |
| **No revocation story** — short TTLs help, but what about immediate compromise? | Maintain a CRL or use OCSP stapling; many meshes use TTL ≤1h as the primary revocation mechanism |
| **Plaintext fallback** — if mTLS config fails, does the call go through unencrypted? | Design for **fail-closed**, not fail-open |
| **mTLS but no authZ** — anyone with a valid mesh cert can call anything | Always layer authorization policies on top |
| **Logging the wrong identity** — logs show IP:port, not SPIFFE identity | Log the `X-Forwarded-Client-Cert` header or SPIFFE ID from the verified certificate |

---

## Typical Implementation Stack (Kubernetes)

```
┌──────────────────────────────────────────────┐
│                   Istio / Linkerd            │
│  ┌──────────────┐  ┌──────────────────────┐  │
│  │ cert-manager │  │ SPIRE / Vault Agent  │  │
│  │ (CA certs)   │  │ (identity issuance)  │  │
│  └──────────────┘  └──────────────────────┘  │
│  ┌────────────────────────────────────────┐  │
│  │ Envoy / linkerd-proxy (data plane)     │  │
│  │  • mTLS termination                    │  │
│  │  • Certificate hot-reload              │  │
│  │  • Authorization policy enforcement    │  │
│  └────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
```

---

## Summary

mTLS provides **transport authentication** between microservices: both parties prove who they are, and all traffic is encrypted. In practice, it's almost always delivered through a service mesh (Istio/Linkerd/Consul) rather than hand-rolled per application — the mesh handles certificate issuance, rotation, mTLS termination, and authorization policy enforcement transparently. The core design decisions are: sidecar vs. ambient vs. app-level; short-lived certs with automated rotation; and always pairing mTLS identity with explicit authorization policies.
