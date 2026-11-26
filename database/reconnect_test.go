package database_test

import (
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
	"github.com/valentin-kaiser/go-core/internal/await"
)

const (
	reconnectCheckInterval = 20 * time.Millisecond
	poolReplaceTimeout     = 5 * time.Second
)

// A reconnect replaces the connection pool. The replaced pool must be closed,
// otherwise its idle connections (and the goroutine that opens connections for
// it) stay around for the life of the process, once per reconnect.
func TestReconnectClosesReplacedPool(t *testing.T) {
	db := database.New[TestQueries](database.DriverSQLite, "reconnect-close")
	db.RegisterQueries(NewTestQueries)
	db.Connect(reconnectCheckInterval, memoryDSN)
	cleanupDisconnect(t, db)
	db.AwaitConnection()

	old := db.Get()
	err := old.Ping()
	if err != nil {
		t.Fatalf("ping before reconnect: %v", err)
	}

	db.Reconnect(memoryDSN)
	await.Until(t, poolReplaceTimeout, func() bool { return db.Get() != old }, "the pool to be replaced after Reconnect")

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
