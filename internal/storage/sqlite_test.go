package storage

import (
	"testing"
	"time"

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

func TestMemoryCache(t *testing.T) {
	cache := NewMemoryCache()

	// Test Set and Get
	cache.Set("key1", "value1", 1*time.Hour)
	val, ok := cache.Get("key1")
	if !ok || val != "value1" {
		t.Fatalf("Failed to get cached value")
	}

	// Test expiration
	cache.Set("key2", "value2", 1*time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	_, ok = cache.Get("key2")
	if ok {
		t.Fatalf("Expected expired key to be removed")
	}

	// Test Delete
	cache.Set("key3", "value3", 1*time.Hour)
	cache.Delete("key3")
	_, ok = cache.Get("key3")
	if ok {
		t.Fatalf("Expected deleted key to be gone")
	}

	// Test Len (including expired entries)
	cache.Clear() // Start fresh
	cache.Set("key4", "value4", 1*time.Hour)
	cache.Set("key5", "value5", 1*time.Hour)
	if cache.Len() != 2 {
		t.Fatalf("Expected 2 entries, got %d", cache.Len())
	}

	// Test Clear
	cache.Clear()
	if cache.Len() != 0 {
		t.Fatalf("Expected 0 entries after clear, got %d", cache.Len())
	}
}
