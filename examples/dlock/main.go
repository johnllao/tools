// dlock is a distributed lock backed by a cluster of peer servers.
// Each server holds independent lock state. A lock is acquired only when a
// strict majority (> N/2) of servers grant it — this resists up to (N-1)/2
// node failures without a leader or external dependency.
//
// Pure Go standard library — no Redis, no etcd, no ZooKeeper.
//
// ---------------------------------------------------------------------------
// Quick start (3 nodes)
// ---------------------------------------------------------------------------
//
//	# Terminal 1
//	go run ./examples/dlock -port 8001 -peers localhost:8001,localhost:8002,localhost:8003
//
//	# Terminal 2
//	go run ./examples/dlock -port 8002 -peers localhost:8001,localhost:8002,localhost:8003
//
//	# Terminal 3
//	go run ./examples/dlock -port 8003 -peers localhost:8001,localhost:8002,localhost:8003
//
// Acquire a lock:
//
//	curl -s -X POST localhost:8001/acquire \
//	  -d '{"name":"my-lock","owner":"job-1","ttl_secs":30}' | jq
//
// Release a lock:
//
//	curl -s -X POST localhost:8001/release \
//	  -d '{"name":"my-lock","owner":"job-1"}' | jq
//
// Status:
//
//	curl -s localhost:8001/status | jq
//
// ---------------------------------------------------------------------------
// How it works
// ---------------------------------------------------------------------------
//
//	Client                  Node A       Node B       Node C
//	  |                       |            |            |
//	  |--- POST /acquire ---->|            |            |
//	  |                       |-- try -----|            |
//	  |                       |-- try ----------------->|
//	  |                       |<-- ok -----|            |
//	  |                       |<-- ok ------------------|
//	  |                       |                        |
//	  |   votes=2, quorum=2   |                        |
//	  |<-- 200 {"acquired":true}                        |
//	  |                       |                        |
//
// If a node grants a lock but the quorum fails, the acquirer sends release
// requests to every node that voted yes (rollback), preventing orphaned locks.
//
// ---------------------------------------------------------------------------
// Failure modes
// ---------------------------------------------------------------------------
//
//   - Minority crash: cluster continues. 3-node cluster survives 1 crash.
//   - Majority crash: no new locks can be acquired (safety over availability).
//   - Network partition: minority partition can't grant locks; majority
//     partition continues normally.
//   - Expired lock: locks carry a TTL; each node purges expired entries on
//     status queries. A new acquirer can claim an expired lock.
//   - Rollback failure: if a rollback release fails after a partial acquire,
//     the orphaned lock expires naturally via TTL.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	// port is the HTTP port this node listens on for peer-to-peer lock
	// coordination requests. Each node in the cluster should use a unique port
	// when running on the same host.
	port = flag.Int("port", 8001, "HTTP port for this node")

	// peers is a comma-separated list of all cluster peer addresses in
	// host:port format, including self. This node uses the list to discover
	// other nodes and to coordinate distributed lock acquisition. All nodes
	// in the cluster must be started with the same peer list.
	peers = flag.String("peers", "localhost:8001", "Comma-separated list of all cluster peers (including self)")
)

// lockEntry represents a single lock held in a node's local lock table.
// Each entry records the lock name, the current owner, when it was acquired,
// and when it expires. Expired entries are eligible for reclamation by any
// caller and are purged on status queries.
//
// This is a plain data record — all synchronization is handled by
// [MajorityLock].
type lockEntry struct {
	// Name is the unique identifier for the lock resource (e.g. "my-lock").
	// It is the key used to coordinate ownership across the cluster.
	Name string `json:"name"`

	// Owner identifies the entity that holds this lock (e.g. "job-1",
	// "worker-7"). Release and re-acquire checks compare Owner to prevent
	// one client from releasing another's lock.
	Owner string `json:"owner"`

	// Acquired is the wall-clock time when this lock was granted by the
	// local node. Informational — the TTL is driven by ExpiresAt.
	Acquired time.Time `json:"acquired"`

	// ExpiresAt is the wall-clock deadline after which this lock is
	// considered expired. Set at acquire time as Acquired + TTL.
	// Expired locks are purged on status queries and can be claimed by a
	// new owner without an explicit release.
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the lock's TTL has elapsed by comparing the
// wall-clock deadline ([lockEntry.ExpiresAt]) against the current time.
// An expired lock can be claimed by a new owner without an explicit
// release — [MajorityLock.TryAcquire] treats an expired entry as free,
// and [MajorityLock.Status] purges expired entries on each call.
func (le lockEntry) Expired() bool {
	return time.Now().After(le.ExpiresAt)
}

// MajorityLock is a single node's local lock table — an in-memory key/value
// store mapping lock names to [lockEntry] records, protected by a mutex.
// It exposes three operations that peers invoke via HTTP:
//
//   - [MajorityLock.TryAcquire] — grant a lock if the name is free or the
//     previous holder's TTL has expired.
//   - [MajorityLock.Release] — remove a lock if the caller is the current
//     owner.
//   - [MajorityLock.Status] — return a snapshot of active locks, purging
//     expired entries as a side effect.
//
// MajorityLock has no awareness of the cluster or quorum; it answers every
// request independently based on its own local state. The quorum coordination
// — broadcasting to peers, counting votes, and rolling back on failure — is
// implemented by [Server] in the HTTP layer. This separation keeps the lock
// table simple and testable in isolation.
type MajorityLock struct {
	mu    sync.Mutex
	locks map[string]lockEntry
}

// NewMajorityLock returns an initialized [MajorityLock] with an empty lock
// map, ready to accept [MajorityLock.TryAcquire] and
// [MajorityLock.Release] calls. The returned instance is safe for
// concurrent use — all public methods serialize access via the internal
// mutex.
func NewMajorityLock() *MajorityLock {
	return &MajorityLock{locks: make(map[string]lockEntry)}
}

// TryAcquire attempts to set a lock entry in this node's local table.
// It returns true if the lock was granted and false if a different active
// owner already holds it.
//
// A lock is granted when:
//   - The name is not currently in the table, OR
//   - The previous entry's TTL has expired (see [lockEntry.Expired]).
//
// When granted, the entry is recorded with the given name, owner, and
// wall-clock deadline (now + ttlSecs). The caller ([Server]) is
// responsible for aggregating votes across peers to reach quorum; a
// grant from TryAcquire is a single vote, not a final decision.
//
// This method is safe for concurrent use.
func (ml *MajorityLock) TryAcquire(name, owner string, ttlSecs int) bool {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	existing, ok := ml.locks[name]
	if ok && !existing.Expired() {
		return false // held by an active owner
	}
	ml.locks[name] = lockEntry{
		Name:      name,
		Owner:     owner,
		Acquired:  time.Now(),
		ExpiresAt: time.Now().Add(time.Duration(ttlSecs) * time.Second),
	}
	return true
}

// Release removes a lock entry from this node's local table.
// It returns true if the lock was found and the caller is the current
// owner, meaning the entry was deleted. It returns false if:
//   - No entry exists for the given name, OR
//   - The entry exists but is held by a different owner.
//
// Ownership is checked by comparing the caller-supplied owner string
// against the [lockEntry.Owner] field — this prevents one client from
// releasing another's lock.
//
// Unlike [MajorityLock.TryAcquire], Release does not consider expiry.
// An expired lock that still sits in the table (not yet purged by
// [MajorityLock.Status]) can be released by its original owner but
// cannot be released by a different caller.
//
// This method is safe for concurrent use.
func (ml *MajorityLock) Release(name, owner string) bool {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	entry, ok := ml.locks[name]
	if !ok || entry.Owner != owner {
		return false
	}
	delete(ml.locks, name)
	return true
}

// Status returns a snapshot of all active (non-expired) locks currently
// held in this node's local table.
//
// As a side effect, Status purges expired entries before building the
// result — this is the only mechanism that removes expired locks from
// the table. Purging on read means no background goroutine or timer is
// needed; expired entries accumulate harmlessly until the next status
// query or until [MajorityLock.TryAcquire] overwrites them.
//
// The returned slice is a fresh copy; callers may safely mutate it.
//
// This method is safe for concurrent use.
func (ml *MajorityLock) Status() []lockEntry {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	for k, v := range ml.locks {
		if v.Expired() {
			delete(ml.locks, k)
		}
	}
	out := make([]lockEntry, 0, len(ml.locks))
	for _, v := range ml.locks {
		out = append(out, v)
	}
	return out
}

// acquireReq is the JSON body for the client-facing POST /acquire
// endpoint. The receiving [Server] forwards the same payload to every
// peer's POST /try-acquire endpoint, collects votes, and returns an
// [acquireResp].
//
// Fields:
//
//   - Name is the lock resource to acquire (e.g. "my-lock").
//   - Owner is the caller-chosen identifier that must be supplied again
//     to release the lock. Typically a job ID, worker name, or instance
//     ID.
//   - TTLSecs is the time-to-live in seconds. If the owner crashes
//     before releasing, the lock automatically expires after this
//     duration, preventing deadlock.
type acquireReq struct {
	Name    string `json:"name"`
	Owner   string `json:"owner"`
	TTLSecs int    `json:"ttl_secs"`
}

// acquireResp is the JSON response returned by POST /acquire.
// It summarizes the result of a quorum vote across all peers.
//
// Fields:
//
//   - Acquired is true when the number of peer votes (Votes) meets or
//     exceeds the strict-majority threshold (Quorum = floor(N/2) + 1).
//     When false, HeldBy names the current owner (if known) and Errors
//     contains per-peer failure details.
//   - Votes is the count of peers that granted the lock in response to
//     the /try-acquire broadcast. Always populated even on failure —
//     Server uses this to issue rollback releases to the peers that
//     voted yes.
//   - Quorum is the strict-majority threshold for this cluster
//     (floor(N/2) + 1), included so the client can understand the
//     voting result without knowing the cluster size.
//   - HeldBy is populated when Acquired is false and the lock is
//     currently held. Identifies the owning client so the caller can
//     decide whether to wait or fail fast.
//   - Errors collects per-peer error messages (connection refused,
//     timeout, etc.). Non-empty only when some peers were unreachable;
//     acquiring can still succeed if quorum is met despite errors.
type acquireResp struct {
	Acquired bool     `json:"acquired"`
	Votes    int      `json:"votes,omitempty"`
	Quorum   int      `json:"quorum,omitempty"`
	HeldBy   string   `json:"held_by,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

// releaseReq is the JSON body for the client-facing POST /release
// endpoint. The receiving [Server] broadcasts this payload to every
// peer's POST /try-release endpoint (best-effort — individual peer
// failures are logged but do not fail the overall release).
//
// It is also used for rollback: when an acquire request fails to reach
// quorum after some peers voted yes, [Server] sends releaseReq to each
// peer that granted a vote, preventing orphaned locks.
//
// Fields:
//
//   - Name is the lock resource to release.
//   - Owner must match the owner string supplied at acquire time. If it
//     doesn't, the peer's [MajorityLock.Release] refuses the request.
type releaseReq struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

// tryResp is the JSON response returned by internal peer endpoints
// POST /try-acquire and POST /try-release. It is not exposed to
// external clients — [Server] uses it internally to count votes when
// building an [acquireResp].
//
// The single boolean field keeps the peer protocol minimal and
// unambiguous: each peer either grants/releases the lock (true) or
// refuses (false). All quorum logic, error aggregation, and rollback
// decisions live in the caller ([Server]), not in the peers.
type tryResp struct {
	OK bool `json:"ok"`
}

// Server is the HTTP layer that implements quorum-based distributed
// locking on top of a local [MajorityLock] instance.
//
// Server exposes two sets of endpoints:
//
// Client-facing (external):
//
//	POST /acquire  — acquire a lock via strict-majority vote across all peers
//	POST /release  — release a lock (best-effort broadcast to all peers)
//	GET  /status   — list active locks on this node
//
// Internal peer endpoints (called by other Server instances):
//
//	POST /try-acquire — single-node lock grant attempt
//	POST /try-release — single-node lock release attempt
//
// Quorum coordination:
//
// On POST /acquire, Server fans out [acquireReq] to every peer's
// /try-acquire endpoint (including itself). It collects [tryResp] votes,
// computes whether the strict-majority threshold (floor(N/2) + 1) is met,
// and returns an [acquireResp]. If quorum fails after some peers voted
// yes, Server sends rollback releases to those peers to prevent orphaned
// locks.
//
// On POST /release, Server broadcasts to all peers best-effort —
// individual failures are logged but do not fail the request. Locks also
// self-clean via TTL expiry, so a missed release is not catastrophic.
//
// Fields:
//
//   - ml is the local lock table that answers /try-acquire and
//     /try-release for this node.
//   - peers is the full cluster membership list (host:port), including
//     self. It is used both as the fan-out target for acquire/release and
//     to compute the quorum threshold.
//   - self is this node's address in host:port form, used to skip the
//     HTTP round-trip when calling /try-acquire or /try-release locally.
//   - client is the HTTP client used for peer-to-peer requests. It has a
//     short timeout (default 2s) so an unreachable peer doesn't block an
//     acquire indefinitely.
type Server struct {
	ml     *MajorityLock
	peers  []string // all peers including self
	self   string   // e.g. "localhost:8001"
	client *http.Client
}

// NewServer creates a [Server] that wraps the given [MajorityLock] and peer
// configuration.
//
// Parameters:
//   - ml is the local lock table that this node's /try-acquire and
//     /try-release handlers delegate to.
//   - peers is the full cluster membership list (host:port), including
//     self. It must be identical across all nodes in the cluster.
//   - self is this node's address, used to call [MajorityLock] directly
//     instead of making an HTTP request to itself.
//
// NewServer also initializes the internal HTTP client with a 2-second
// timeout — an unreachable peer will not block an acquire for longer than
// this.
func NewServer(ml *MajorityLock, peers []string, self string) *Server {
	return &Server{
		ml:     ml,
		peers:  peers,
		self:   self,
		client: &http.Client{Timeout: 2 * time.Second},
	}
}

// Register attaches all [Server] HTTP handlers to the given [http.ServeMux].
// After registration, the caller typically starts listening with
// [net/http.ListenAndServe].
//
// Routes registered:
//
//   - /acquire      (POST) — client-facing lock acquisition via quorum vote
//   - /release      (POST) — client-facing lock release, best-effort broadcast
//   - /status       (GET)  — client-facing list of active locks on this node
//   - /try-acquire  (POST) — internal peer endpoint for single-node grant
//   - /try-release  (POST) — internal peer endpoint for single-node release
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/acquire", s.handleAcquire)
	mux.HandleFunc("/release", s.handleRelease)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/try-acquire", s.handleTryAcquire) // internal
	mux.HandleFunc("/try-release", s.handleTryRelease) // internal
}

// handleAcquire is the handler for POST /acquire — the client-facing lock
// acquisition endpoint.
//
// Flow:
//  1. Decode the JSON body into an [acquireReq]. Reject non-POST methods
//     and malformed payloads.
//  2. Default TTL to 30 seconds if TTLSecs is unset or non-positive.
//  3. Fan out the request to every peer (including self) via
//     [Server.askTryAcquire], collecting votes in parallel with a
//     [sync.WaitGroup].
//  4. After all peers respond, compare the vote count to the
//     strict-majority threshold (quorum = floor(N/2) + 1).
//  5. If votes ≥ quorum, return 200 with [acquireResp]{Acquired: true}.
//  6. If quorum fails, send rollback release requests to every peer that
//     voted yes (via [Server.askTryRelease]) to prevent orphaned locks,
//     then return 409 Conflict.
//
// Concurrency: each peer is contacted in its own goroutine; a [sync.Mutex]
// serializes access to the shared votes counter and error list.
func (s *Server) handleAcquire(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req acquireReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.TTLSecs <= 0 {
		req.TTLSecs = 30
	}

	// Contact every peer in parallel. Count how many grant the lock.
	quorum := len(s.peers)/2 + 1
	var (
		votes int
		errs  []string
		mu    sync.Mutex
		wg    sync.WaitGroup
	)
	for _, peer := range s.peers {
		wg.Add(1)
		go func(peer string) {
			defer wg.Done()
			ok, err := s.askTryAcquire(peer, req)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", peer, err))
				return
			}
			if ok {
				votes++
			}
		}(peer)
	}
	wg.Wait()

	if votes >= quorum {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(acquireResp{Acquired: true, Votes: votes, Quorum: quorum})
		log.Printf("ACQUIRE %q by %q — %d/%d votes ✓", req.Name, req.Owner, votes, quorum)
	} else {
		// Rollback: release on any peers that granted the lock to avoid
		// orphaned locks from a partial quorum.
		s.rollbackRelease(req.Name, req.Owner)

		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(acquireResp{
			Acquired: false, Votes: votes, Quorum: quorum, Errors: errs,
		})
		log.Printf("ACQUIRE %q by %q — %d/%d votes ✗ (rolled back)", req.Name, req.Owner, votes, quorum)
	}
}

// handleRelease is the handler for POST /release — the client-facing lock
// release endpoint.
//
// Flow:
//  1. Decode the JSON body into a [releaseReq]. Reject non-POST methods
//     and malformed payloads.
//  2. Broadcast the release to every peer (including self) via
//     [Server.askTryRelease] in parallel goroutines.
//  3. Return 200 with {"released": true} regardless of individual peer
//     outcomes.
//
// Release is best-effort: individual peer failures (timeout, connection
// refused) are logged but do not affect the response. This is a deliberate
// design choice — a strict release vote would be as hard as acquire (also
// requiring quorum), which would make release fragile in the face of node
// failures. Instead, any orphaned entries left behind by a failed release
// self-clean via TTL expiry.
//
// The owner check in [MajorityLock.Release] ensures that only the
// legitimate holder can release — a different client sending a release
// for the same lock name is silently rejected by each peer.
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req releaseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Fan out to all peers. Best-effort — we return success even if some nodes
	// are unreachable; the TTL will clean up stragglers.
	var wg sync.WaitGroup
	for _, peer := range s.peers {
		wg.Add(1)
		go func(peer string) {
			defer wg.Done()
			s.askTryRelease(peer, req)
		}(peer)
	}
	wg.Wait()

	json.NewEncoder(w).Encode(map[string]any{"released": true})
	log.Printf("RELEASE %q by %q", req.Name, req.Owner)
}

// handleStatus is the handler for GET /status — a read-only diagnostic
// endpoint that returns the active locks on this node.
//
// Flow:
//  1. Delegate to [MajorityLock.Status], which returns a snapshot of
//     non-expired locks and purges expired entries as a side effect.
//  2. Return 200 with a JSON object containing the node's address and the
//     lock list.
//
// This is a local-only view — it shows locks held by this specific node,
// not the cluster-wide state. To see the full picture, query /status on
// each node. No quorum or peer communication is involved.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	locks := s.ml.Status()
	json.NewEncoder(w).Encode(map[string]any{
		"node":  s.self,
		"locks": locks,
	})
}

// handleTryAcquire is the handler for POST /try-acquire — an internal
// peer endpoint called by other [Server] instances during quorum voting.
// It is not exposed to external clients.
//
// Flow:
//  1. Decode the JSON body into an [acquireReq].
//  2. Delegate to [MajorityLock.TryAcquire] on this node's local lock
//     table.
//  3. Return a [tryResp] with the boolean result — OK=true means this
//     node grants the lock (name is free or previous TTL expired).
//
// Each call to this handler represents a single vote in the quorum
// process. The caller ([Server.handleAcquire]) aggregates these votes
// across all peers to determine whether the strict-majority threshold
// is met.
func (s *Server) handleTryAcquire(w http.ResponseWriter, r *http.Request) {
	var req acquireReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ok := s.ml.TryAcquire(req.Name, req.Owner, req.TTLSecs)
	json.NewEncoder(w).Encode(tryResp{OK: ok})
}

// handleTryRelease is the handler for POST /try-release — an internal
// peer endpoint called by other [Server] instances during lock release
// (both client-initiated and rollback). It is not exposed to external
// clients.
//
// Flow:
//  1. Decode the JSON body into a [releaseReq].
//  2. Delegate to [MajorityLock.Release] on this node's local lock table.
//     The return value is intentionally ignored (see below).
//  3. Always return [tryResp]{OK: true}.
//
// The response is always OK=true regardless of whether the lock existed
// or the owner matched. This is by design: release is best-effort in this
// system. If a node already purged the lock (TTL expiry) or the owner
// doesn't match, there's nothing to do — the caller ([Server.handleRelease]
// or [Server.rollbackRelease]) cares only that the attempt was delivered,
// not its exact local outcome.
func (s *Server) handleTryRelease(w http.ResponseWriter, r *http.Request) {
	var req releaseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.ml.Release(req.Name, req.Owner)
	json.NewEncoder(w).Encode(tryResp{OK: true})
}

// askTryAcquire sends a /try-acquire request to the given peer and returns
// the boolean vote from that peer's lock table.
//
// When peer equals [Server.self], the call is routed directly to
// [MajorityLock.TryAcquire] on the local lock table, avoiding an HTTP
// round-trip. For remote peers, the [acquireReq] is JSON-encoded and sent
// via POST to peer's /try-acquire endpoint.
//
// The returned bool is a single vote — one peer's answer to "should this
// lock be granted?" The caller ([Server.handleAcquire]) aggregates these
// votes across all peers to determine quorum.
//
// Errors (timeout, connection refused, non-2xx response) are returned to
// the caller, which records them in the [acquireResp.Errors] list. A
// single peer error does not prevent quorum from being reached if enough
// other peers vote yes.
func (s *Server) askTryAcquire(peer string, req acquireReq) (bool, error) {
	if peer == s.self {
		return s.ml.TryAcquire(req.Name, req.Owner, req.TTLSecs), nil
	}
	return s.postTry(peer, "/try-acquire", req)
}

// askTryRelease sends a /try-release request to the given peer.
//
// When peer equals [Server.self], the call is routed directly to
// [MajorityLock.Release] on the local lock table, avoiding an HTTP
// round-trip. For remote peers, the [releaseReq] is JSON-encoded and
// sent via POST to peer's /try-release endpoint.
//
// The return value is the result from [MajorityLock.Release] (true if the
// lock was found and owned by the caller, false otherwise). Most callers
// ignore the bool because release is best-effort — see
// [Server.handleTryRelease] for the rationale.
//
// Errors (timeout, connection refused, non-2xx response) are returned to
// the caller, which may log them. In [Server.handleRelease] (client
// release), errors are logged but do not fail the request. In
// [Server.rollbackRelease] (acquire rollback), errors are logged but the
// orphaned lock self-cleans via TTL expiry.
func (s *Server) askTryRelease(peer string, req releaseReq) (bool, error) {
	if peer == s.self {
		return s.ml.Release(req.Name, req.Owner), nil
	}
	return s.postTry(peer, "/try-release", req)
}

// postTry is the shared HTTP transport used by both [Server.askTryAcquire]
// and [Server.askTryRelease] to communicate with remote peers. It is not
// called for the local node — callers check [Server.self] before invoking.
//
// Flow:
//  1. JSON-marshal the request body.
//  2. POST to http://{peer}{path} (e.g. "http://localhost:8002/try-acquire")
//     using [Server.client], which has a 2-second timeout.
//  3. Decode the [tryResp] from the response body and return its OK field.
//
// Error handling:
//   - Marshal failures are returned immediately (should never happen with
//     the known request types).
//   - Connection failures ("unreachable") are wrapped for the caller to
//     log or aggregate in [acquireResp.Errors].
//   - Decode failures indicate a non-conforming peer — the error is
//     returned and treated as a failed vote.
//
// The 2-second timeout (set in [NewServer]) ensures a single unreachable
// peer does not block the entire acquire operation — the caller fans out
// requests in parallel goroutines, so the worst-case latency is one
// timeout period, not N × timeout.
func (s *Server) postTry(peer, path string, body any) (bool, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return false, fmt.Errorf("marshal: %w", err)
	}
	resp, err := s.client.Post(
		"http://"+peer+path,
		"application/json",
		bytes.NewReader(b),
	)
	if err != nil {
		return false, fmt.Errorf("unreachable: %w", err)
	}
	defer resp.Body.Close()

	var result tryResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode: %w", err)
	}
	return result.OK, nil
}

// rollbackRelease sends release requests to every peer after a quorum vote
// fails — i.e., some peers granted the lock but not enough to meet the
// strict-majority threshold. Without this step, those peers would hold
// orphaned locks that block subsequent acquire attempts until TTL expiry.
//
// Flow:
//  1. Build a [releaseReq] with the lock name and the original owner.
//  2. Fan out to every peer (including self) via [Server.askTryRelease]
//     in parallel goroutines.
//  3. Log each peer's result (released, not found, or error).
//
// This is best-effort: individual peer failures are logged but not
// retried. If a rollback release fails to reach a peer, the orphaned lock
// on that peer self-cleans via TTL expiry — the lock is not permanently
// stranded, only temporarily unavailable until its TTL elapses.
//
// Called by [Server.handleAcquire] after a failed quorum vote.
func (s *Server) rollbackRelease(name, owner string) {
	req := releaseReq{Name: name, Owner: owner}
	var wg sync.WaitGroup
	for _, peer := range s.peers {
		wg.Add(1)
		go func(peer string) {
			defer wg.Done()
			if ok, err := s.askTryRelease(peer, req); err != nil {
				log.Printf("[rollback] release %q on %s: %v", name, peer, err)
			} else if ok {
				log.Printf("[rollback] released %q on %s", name, peer)
			}
		}(peer)
	}
	wg.Wait()
}

// main is the entry point for the dlock node. It parses CLI flags, validates
// the configuration, wires up the lock table and HTTP server, and starts
// listening.
//
// Startup sequence:
//  1. Parse -port and -peers flags.
//  2. Build the self address ("localhost:{port}") and verify it appears in
//     the peer list — this is a safety check to prevent misconfiguration.
//  3. Create a [MajorityLock] as the local lock table.
//  4. Create a [Server] with the lock table, peer list, and self address.
//  5. Register all HTTP handlers on a new [http.ServeMux] via
//     [Server.Register].
//  6. Log the node identity, peer set, quorum threshold, and available
//     endpoints.
//  7. Call [net/http.ListenAndServe] — this blocks forever (or until a
//     fatal error).
//
// Each node in the cluster must be started separately with a unique -port
// and identical -peers. See the package doc at the top of this file for
// example invocations.
func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)

	peerList := strings.Split(*peers, ",")
	self := fmt.Sprintf("localhost:%d", *port)

	// Verify self is in the peer list — a node not in the peer list would
	// never vote on its own acquire requests, making quorum impossible.
	found := false
	for _, p := range peerList {
		if p == self {
			found = true
			break
		}
	}
	if !found {
		log.Fatalf("self (%s) must be included in -peers (%s)", self, *peers)
	}

	ml := NewMajorityLock()
	srv := NewServer(ml, peerList, self)

	mux := http.NewServeMux()
	srv.Register(mux)

	addr := fmt.Sprintf(":%d", *port)
	quorum := len(peerList)/2 + 1
	log.Printf("dlock node %s starting (peers: %v, quorum: %d/%d)", self, peerList, quorum, len(peerList))
	log.Printf("endpoints: /acquire /release /status")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
