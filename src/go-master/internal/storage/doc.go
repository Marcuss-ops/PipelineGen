// Package storage provides the persistence layer for the VeloxEditing backend.
//
// It implements a flexible storage architecture supporting multiple backends:
// 1. JSON (Local): Simple file-based storage using JSON files in the canonical runtime data directory. Best for local development.
// 2. PostgreSQL (Production): Robust relational database support for high-concurrency and durable job queues.
//
// The package manages core entities such as:
// - Jobs & Queues: Atomic task management using SKIP LOCKED patterns (in Postgres).
// - State Persistence: Saving and loading application configuration and runtime state.
// - Database Migrations: Managing schema evolution for SQL-based backends.
//
// The storage layer is designed to be agnostic, allowing the business logic to remain
// unchanged regardless of the underlying database technology.
package storage
