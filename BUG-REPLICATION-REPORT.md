# Bug Report: `RemoveUnusedMocks` Deletes Startup Mocks During Auto-Replay

**Date:** March 31, 2026  
**Reporter:** Anju  
**Status:** Replicated ✅  

---

## Table of Contents

1. [Background Concepts](#1-background-concepts)
2. [Bug Hypothesis](#2-bug-hypothesis)
3. [Step-by-Step Breakdown of the Bug](#3-step-by-step-breakdown-of-the-bug)
4. [Replication Environment](#4-replication-environment)
5. [Replication Steps](#5-replication-steps)
6. [Evidence That the Bug Was Replicated](#6-evidence-that-the-bug-was-replicated)
7. [Root Cause](#7-root-cause)
8. [Impact](#8-impact)

---

## 1. Background Concepts

Before diving in, let's define a few terms used throughout this report:

- **Recording:** When Keploy intercepts all network traffic (HTTP requests, DNS lookups, TLS handshakes, etc.) going in and out of your application and saves them. Incoming requests become **test cases**; outgoing calls become **mocks**.

- **Test-set:** A collection of test cases and their mocks that were recorded from one specific pod. If you have 2 pods, you get 2 test-sets.

- **Mock:** A saved copy of an outgoing network call (request + response). During replay, Keploy returns the saved response instead of actually calling the external service.

- **Startup mocks (init traffic):** Outgoing network calls that happen **only once** — when the application first starts. Examples: initial database connection, fetching config from an external API, TLS handshake with a remote server. These are recorded as mocks for **every** pod.

- **Auto-replay (in-cluster):** Keploy replays all test-sets against the application automatically inside the Kubernetes cluster. The application runs as a **single standalone pod** (not the original multi-replica deployment).

- **`RemoveUnusedMocks`:** A cleanup function that runs after replay. It deletes any mock that was **not consumed** during the test run, assuming they are stale/irrelevant. This is the function that causes the bug.

---

## 2. Bug Hypothesis

> **When multiple test-sets are replayed against a single application instance (without restarting the app between test-sets), startup mocks in the second (and later) test-sets are never consumed — because the app only starts once. `RemoveUnusedMocks` then incorrectly deletes these unconsumed startup mocks from the database. On subsequent replays, those test-sets fail because the startup mocks are gone.**

### Why does this happen?

The core issue is a **mismatch between recording and replay behavior:**

| Phase | Pods | Startup Calls |
|-------|------|---------------|
| **Recording** | 2 pods (one per replica) | Each pod starts independently → each records its own startup mocks |
| **Replay** | 1 pod (standalone) | App starts **once** → startup mocks consumed for **only the first** test-set |

The second test-set's startup mocks sit unused → `RemoveUnusedMocks` deletes them → they're gone from the database forever.

---

## 3. Step-by-Step Breakdown of the Bug

Here is exactly what happens, step by step:

### During Recording (in-cluster, 2 replicas)

```
Pod-A starts → makes startup calls (GET /posts, GET /users) → recorded as mocks in test-set-A
Pod-B starts → makes startup calls (GET /posts, GET /users) → recorded as mocks in test-set-B

User hits the service → traffic is load-balanced across Pod-A and Pod-B
  → Pod-A records: test-1, test-2 (in test-set-A)
  → Pod-B records: test-3, test-4 (in test-set-B)

Both test-sets have:
  ✅ Startup mocks (GET /posts, GET /users, DNS, TLS)
  ✅ Per-request mocks (GET /posts/1, GET /posts/2)
  ✅ Test cases (the actual HTTP requests)
```

### During Auto-Replay (standalone, 1 pod)

```
Step 1: Replay test-set-A
  ├── App STARTS → makes startup calls → consumes test-set-A's startup mocks ✅
  ├── test-1 runs → consumes per-request mock → PASS ✅
  ├── test-2 runs → consumes per-request mock → PASS ✅
  ├── test-3 runs → consumes per-request mock → PASS ✅
  └── test-4 runs → consumes per-request mock → PASS ✅
  └── All mocks consumed → RemoveUnusedMocks: nothing to remove ✅

Step 2: Replay test-set-B (app is NOT restarted)
  ├── App is ALREADY RUNNING → no startup calls happen
  ├── test-1 runs → consumes per-request mock → PASS ✅
  ├── test-2 runs → consumes per-request mock → PASS ✅
  ├── test-3 runs → consumes per-request mock → PASS ✅
  └── test-4 runs → consumes per-request mock → PASS ✅
  └── ⚠️ Startup mocks were NEVER consumed
  └── 🐛 RemoveUnusedMocks DELETES test-set-B's startup mocks from the DB

Step 3: Mocks saved to DB — test-set-B now has NO startup mocks 💀
```

### On Any Subsequent Replay

```
Step 1: Replay test-set-B (or whichever ran second — it no longer has startup mocks)
  ├── App STARTS → makes startup calls → ❌ NO MATCHING MOCK FOUND
  ├── App gets garbage response from Keploy proxy → parse error → APP CRASHES
  ├── test-1 → connection reset → FAIL ❌
  ├── test-2 → connection reset → FAIL ❌
  ├── test-3 → connection reset → FAIL ❌
  └── test-4 → connection reset → FAIL ❌
```

---

## 4. Replication Environment

### App: `sample-pg-app` (purpose-built for this bug)

A minimal Go HTTP server designed to produce **obvious startup traffic**:

| Component | Details |
|-----------|---------|
| **Language** | Go 1.22 |
| **Startup calls** | `GET https://jsonplaceholder.typicode.com/posts?_limit=3` and `GET https://jsonplaceholder.typicode.com/users?_limit=2` — both HTTPS, producing DNS + TLS + HTTP mocks |
| **Why startup calls?** | The app calls an external API at boot to fetch config. If this call fails (no mock available), the app crashes with `log.Fatalf` |
| **Endpoints** | `/healthz` (liveness), `/config` (returns startup data), `/users` (list, makes external call), `/users/create` (POST, makes external call) |
| **Per-request calls** | `GET https://jsonplaceholder.typicode.com/posts/1` and `GET .../posts/2` — these mocks ARE consumed correctly during replay |

### Why this app was chosen:

1. **No database needed** — uses in-memory storage, so zero setup complexity
2. **Clear startup traffic** — HTTPS calls to a public API that produce DNS + TLS + HTTP mocks
3. **Fatal on startup failure** — if the startup mock is missing, the app crashes immediately (makes the bug unmistakable)
4. **Per-request external calls** — proves that non-startup mocks work fine (isolates the bug)

### Kubernetes Setup

| Setting | Value |
|---------|-------|
| **Cluster** | Kind (local), cluster name: `Anju` |
| **Deployment** | `k8s-bug-removeunused` |
| **Replicas** | `2` — this is the key setting. Two replicas = two pods = two test-sets |
| **Namespace** | `default` |
| **Image** | `k8s-bug-removeunused:latest` (locally built, loaded into Kind) |
| **Keploy mode** | Enterprise, staging API server (`api.staging.keploy.io`) |

### Deployment Manifest (`k8s.yaml`)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8s-bug-removeunused
spec:
  replicas: 2                    # ← Two pods = two test-sets
  selector:
    matchLabels:
      app: k8s-bug-removeunused
  template:
    spec:
      containers:
        - name: app
          image: k8s-bug-removeunused:latest
          ports:
            - containerPort: 8080
```

---

## 5. Replication Steps

### Step 1: Build and deploy the sample app with 2 replicas

```bash
# Build the Docker image
docker build -t k8s-bug-removeunused:latest -f Dockerfile .

# Load into Kind cluster
kind load docker-image k8s-bug-removeunused:latest

# Deploy with 2 replicas
kubectl apply -f k8s.yaml
```

### Step 2: Record traffic with Keploy (in-cluster)

With the k8s-proxy running and Keploy intercepting traffic, send requests to the app:

```bash
# Hit the service multiple times — traffic is load-balanced across both pods
curl http://<service-ip>/healthz
curl http://<service-ip>/config
curl -X POST http://<service-ip>/users/create -d '{"name":"alice","email":"alice@test.com"}'
curl http://<service-ip>/users
```

Each pod recorded its own test-set:
- `test-set-k8s-bug-removeunused-666757788d-2xbgp` (from Pod A)
- `test-set-k8s-bug-removeunused-666757788d-cfch7` (from Pod B)

Both test-sets contain **4 test cases** and importantly, both contain **startup mocks** (the `GET /posts` and `GET /users` calls to jsonplaceholder.typicode.com).

### Step 3: Trigger auto-replay (first time, in-cluster)

Auto-replay ran in-cluster with a single standalone pod. During this replay:
- Test-set `2xbgp` ran first → app started → startup mocks consumed → all 4 tests passed
- Test-set `cfch7` ran second → app NOT restarted → startup mocks NOT consumed → tests passed (because per-request mocks worked)
- **`RemoveUnusedMocks` deleted the unconsumed startup mocks from test-set `cfch7`** → saved to DB

### Step 4: Run replay again (standalone, to prove the damage)

```bash
sudo -E /home/anju/enterprise/keploy cloud replay \
  --app "default.k8s-bug-removeunused" \
  --cluster "Anju" \
  --freezeTime=false
```

This is where we see the bug in action.

---

## 6. Evidence That the Bug Was Replicated

### Test-set `2xbgp` — Ran first → All PASSED ✅

The app started fresh. Startup mocks were available and consumed. All 4 tests passed:

```
TESTRUN SUMMARY. For test-set: "test-set-k8s-bug-removeunused-666757788d-2xbgp"
    Total tests:        4
    Total test passed:  4
    Total test failed:  0
```

### Test-set `cfch7` — Ran second → All FAILED ❌

The app started fresh (separate docker compose run), but **startup mocks were missing** because `RemoveUnusedMocks` had deleted them in the previous auto-replay:

**Key error in the logs:**
```
keploy-v3-1e8d | ERROR  failed to mock the outgoing message
    {"Destination IP Address": "172.67.167.151:443", "error": "no matching mock found for GET /posts"}
```

The app couldn't find a mock for `GET /posts` (the startup config call). Without the mock, Keploy returned an error response, and the app tried to JSON-parse it:

```
app | startup config parse failed: invalid character 'k' looking for beginning of value
app exited with code 1
```

The `'k'` character is the start of the Keploy error message (`"keploy: no matching mock..."`), not valid JSON.

Since the app crashed, all 4 test cases failed with **status 0** (connection reset — no app to connect to):

```
TESTRUN SUMMARY. For test-set: "test-set-k8s-bug-removeunused-666757788d-cfch7"
    Total tests:        4
    Total test passed:  0
    Total test failed:  4
```

### Overall result:

```
COMPLETE TESTRUN SUMMARY.
    Total tests: 8
    Total test passed: 4
    Total test failed: 4

    "test-set-...-cfch7"  →  4 total, 0 passed, 4 failed ❌
    "test-set-...-2xbgp"  →  4 total, 4 passed, 0 failed ✅
```

### Why this proves the bug:

1. Both test-sets were recorded identically (same app, same ReplicaSet, same traffic pattern)
2. The **only difference** is that `cfch7`'s startup mocks were deleted by `RemoveUnusedMocks` after the first auto-replay
3. When `cfch7` runs now, the app attempts its startup `GET /posts` call, finds no mock, gets garbage, and crashes
4. If startup mocks had NOT been deleted, `cfch7` would have passed just like `2xbgp`

---

## 7. Root Cause

The `RemoveUnusedMocks` function operates on a simple rule:

> "If a mock was not consumed during replay, it is stale — delete it."

This rule is **wrong for startup/init mocks** in the multi-test-set + single-app-instance scenario because:

1. **Recording:** Each pod records its own startup mocks (correctly)
2. **Replay:** Only ONE app instance runs, so startup mocks are consumed for only the first test-set
3. **Cleanup:** `RemoveUnusedMocks` sees unconsumed startup mocks in the second test-set and deletes them
4. **Result:** Second test-set permanently loses its startup mocks

The function does not distinguish between:
- **Truly stale mocks** (from old code, deprecated endpoints) — safe to delete
- **Startup mocks that weren't consumed because the app wasn't restarted** — NOT safe to delete

---

## 8. Impact

| Severity | Description |
|----------|-------------|
| **Data loss** | Startup mocks are permanently deleted from the database. They cannot be recovered without re-recording. |
| **Silent corruption** | The first auto-replay appears to succeed (all tests pass). The damage only surfaces on subsequent replays. |
| **Affects all multi-replica apps** | Any deployment with `replicas > 1` that has startup traffic (DB connections, config fetches, health checks) is vulnerable. |
| **User confusion** | Tests that were passing suddenly start failing without any code change. The error messages (parse failures, connection resets) don't point to the real cause. |

---

*Report generated from standalone replay run on March 31, 2026.*
*Test report link: https://app.staging.keploy.io/tr/1eeb2fc8-d7c6-44f0-bc67-6527981c56f3*
