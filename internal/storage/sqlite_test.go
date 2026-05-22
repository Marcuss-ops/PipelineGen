package storage

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewSQLiteDB_InMemory(t *testing.T) {
	log, _ := zap.NewDevelopment()

	db, err := NewSQLiteDB("", ":memory:", log)
	if err != nil {
		t.Fatalf("Failed to create in-memory database: %v", err)
	}
	defer db.Close()

	// Verify we can ping the database
	if err := db.DB.Ping(); err != nil {
		t.Fatalf("Failed to ping in-memory database: %v", err)
	}

	// Verify we can execute queries
	_, err = db.DB.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Insert and query
	_, err = db.DB.Exec("INSERT INTO test (name) VALUES (?)", "hello")
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	var name string
	err = db.DB.QueryRow("SELECT name FROM test WHERE id = 1").Scan(&name)
	if err != nil {
		t.Fatalf("Failed to query: %v", err)
	}

	if name != "hello" {
		t.Fatalf("Expected 'hello', got '%s'", name)
	}
}


