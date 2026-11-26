package database_test

import (
	"database/sql"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/database"
)

const queryRounds = 2

func connectedTyped(t *testing.T) *database.Connection[TestQueries] {
	t.Helper()
	db := database.New[TestQueries](database.DriverSQLite, "typed")
	db.RegisterQueriesFunc(func(c database.DBTX) *TestQueries { return NewTestQueries(c) })
	db.Connect(t.Context(), connectInterval, memoryDSN)
	cleanupDisconnect(t, db)
	awaitDatabase(t, db)
	return db
}

func TestRegisterQueriesFuncQueryAndTransaction(t *testing.T) {
	db := connectedTyped(t)

	called := false
	err := db.Query(t.Context(), func(q *TestQueries) error {
		called = q != nil
		return nil
	})
	if err != nil || !called {
		t.Fatalf("Query: err=%v called=%v", err, called)
	}

	called = false
	err = db.QueryTransaction(t.Context(), func(q *TestQueries) error {
		called = q != nil
		return nil
	})
	if err != nil || !called {
		t.Fatalf("QueryTransaction: err=%v called=%v", err, called)
	}
	err = db.Transaction(t.Context(), func(_ *sql.Tx) error { return nil })
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
}

// The reflective constructor gets the live connection (the cached argument is keyed by it).
func TestRegisterQueriesReflective(t *testing.T) {
	db := database.New[TestQueries](database.DriverSQLite, "reflect")
	db.RegisterQueries(NewTestQueries)
	cleanupDisconnect(t, db)
	db.Connect(t.Context(), connectInterval, memoryDSN)
	awaitDatabase(t, db)

	for i := range queryRounds {
		var got *TestQueries
		err := db.Query(t.Context(), func(q *TestQueries) error {
			got = q
			return nil
		})
		if err != nil || got == nil {
			t.Fatalf("round %d: err=%v queries=%v", i, err, got)
		}
		err = db.Get().Ping()
		if err != nil {
			t.Fatalf("round %d: connection: %v", i, err)
		}
	}
}
