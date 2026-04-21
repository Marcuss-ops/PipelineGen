# Postgres + Queue migration plan

## Target state
- Postgres = source of truth
- SQLite = local cache only
- Redis Streams or NATS = async transport
- JSON files = dev fallback / import-export only

## Tables to introduce first
- jobs
- job_events
- workers
- worker_heartbeats
- leases
- outbox_events

## First code moves
- isolate repository interfaces from JSON implementations
- isolate queue transport contract from `queue.json`
- make the app bootstrap choose backend by config/env

## Safety rules
- migrations must be additive first
- keep JSON import path for rollback/testing
- keep SQLite out of authority path
- keep queue idempotency in the job layer

## Fast implementation order
1. migrations directory and schema bootstrap
2. postgres job repository
3. postgres worker repository
4. queue contract + redis streams transport
5. fallback json transport only for dev
6. remove production dependence on queue.json
