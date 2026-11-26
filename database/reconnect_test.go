package database_test

import (
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
)

// A reconnect replaces the connection pool. The replaced pool must be closed,
// otherwise its idle connections (and the goroutine that opens connections for
// it) stay around for the life of the process, once per reconnect.
func TestReconnectClosesReplacedPool(t *testing.T) {
	db := database.New[TestQueries](database.DriverSQLite, "reconnect-close")
	db.RegisterQueries(NewTestQueries)
	db.Connect(20*time.Millisecond, ":memory:")
	t.Cleanup(func() { _ = db.Disconnect() })
	db.AwaitConnection()

	old := db.Get()
	err := old.Ping()
	if err != nil {
		t.Fatalf("ping before reconnect: %v", err)
	}

	db.Reconnect(":memory:")
	deadline := time.Now().Add(5 * time.Second)
	for db.Get() == old {
		if time.Now().After(deadline) {
			t.Fatal("pool was not replaced after Reconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}

	err = old.Ping()
	if err == nil {
		t.Fatal("the replaced pool is still open after reconnect")
	}
	db.AwaitConnection()
	err = db.Get().Ping()
	if err != nil {
		t.Fatalf("new pool is not usable: %v", err)
	}
}
