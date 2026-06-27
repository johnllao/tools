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
