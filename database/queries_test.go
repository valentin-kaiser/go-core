package database_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
)

func connectedTyped(t *testing.T) *database.Database[TestQueries] {
	t.Helper()
	db := database.New[TestQueries](database.DriverSQLite, "typed")
	db.RegisterQueriesFunc(func(c database.DBTX) *TestQueries { return NewTestQueries(c) })
	db.Connect(100*time.Millisecond, ":memory:")
	t.Cleanup(func() { _ = db.Disconnect() })
	db.AwaitConnection()
	return db
}

func TestRegisterQueriesFuncQueryAndTransaction(t *testing.T) {
	db := connectedTyped(t)

	called := false
	if err := db.Query(func(q *TestQueries) error { called = q != nil; return nil }); err != nil || !called {
		t.Fatalf("Query: err=%v called=%v", err, called)
	}

	called = false
	if err := db.QueryTransaction(func(q *TestQueries) error { called = q != nil; return nil }); err != nil || !called {
		t.Fatalf("QueryTransaction: err=%v called=%v", err, called)
	}
	if err := db.Transaction(func(tx *sql.Tx) error { return nil }); err != nil {
		t.Fatalf("Transaction: %v", err)
	}
}

// The reflective constructor gets the live connection (the cached argument is keyed by it).
func TestRegisterQueriesReflective(t *testing.T) {
	db := database.New[TestQueries](database.DriverSQLite, "reflect")
	db.RegisterQueries(NewTestQueries)
	t.Cleanup(func() { _ = db.Disconnect() })
	db.Connect(100*time.Millisecond, ":memory:")
	db.AwaitConnection()

	for i := 0; i < 2; i++ {
		var got *TestQueries
		if err := db.Query(func(q *TestQueries) error { got = q; return nil }); err != nil || got == nil {
			t.Fatalf("round %d: err=%v queries=%v", i, err, got)
		}
		if err := db.Get().Ping(); err != nil {
			t.Fatalf("round %d: connection: %v", i, err)
		}
	}
}
