# sample-pg-app

Minimal Go HTTP server that makes HTTPS calls at startup and on each request. Built to reproduce a specific Keploy bug where `RemoveUnusedMocks` deletes startup mocks during multi-test-set auto-replay.

## What the app does

- On startup, fetches data from `https://jsonplaceholder.typicode.com` (`/posts` and `/users`). If these calls fail, the app crashes.
- Exposes four HTTP endpoints:
  - `GET /healthz` — liveness check
  - `GET /config` — returns startup data
  - `GET /users` — lists in-memory users (makes an external call)
  - `POST /users/create` — creates a user (makes an external call)

## Run locally

```bash
go run main.go
```

## Run in Kubernetes (Kind)

```bash
docker build -t k8s-bug-removeunused:latest .
kind load docker-image k8s-bug-removeunused:latest
kubectl apply -f k8s.yaml
```

The deployment uses **2 replicas** so Keploy records 2 test-sets (one per pod).
