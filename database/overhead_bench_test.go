package database_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
)

// These benchmarks isolate what database.Query / Transaction add around a
// statement: the callback does no SQL, so what is measured is the wrapper
// (RLock, connected check, recover defer, reflective query-set construction).
// The raw baseline is a direct call of the same constructor.

func overheadDB(b *testing.B) *database.Database[TestQueries] {
	b.Helper()
	db := database.New[TestQueries](database.DriverSQLite, "overhead")
	db.RegisterQueries(NewTestQueries)
	db.Connect(100*time.Millisecond, ":memory:")
	b.Cleanup(func() { _ = db.Disconnect() })
	db.AwaitConnection()
	return db
}

func BenchmarkOverheadDirectConstructor(b *testing.B) {
	db := overheadDB(b)
	raw := db.Get()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := NewTestQueries(raw)
		_ = q
	}
}

func BenchmarkOverheadQueryNoop(b *testing.B) {
	db := overheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.Query(func(q *TestQueries) error { return nil })
	}
}

func BenchmarkOverheadQueryNoopParallel(b *testing.B) {
	db := overheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = db.Query(func(q *TestQueries) error { return nil })
		}
	})
}

func BenchmarkOverheadTransactionNoop(b *testing.B) {
	db := overheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.Transaction(func(tx *sql.Tx) error { return nil })
	}
}

func BenchmarkOverheadQueryTransactionNoop(b *testing.B) {
	db := overheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.QueryTransaction(func(q *TestQueries) error { return nil })
	}
}

func typedOverheadDB(b *testing.B) *database.Database[TestQueries] {
	b.Helper()
	db := database.New[TestQueries](database.DriverSQLite, "overhead-typed")
	db.RegisterQueriesFunc(func(c database.DBTX) *TestQueries { return NewTestQueries(c) })
	db.Connect(100*time.Millisecond, ":memory:")
	b.Cleanup(func() { _ = db.Disconnect() })
	db.AwaitConnection()
	return db
}

func BenchmarkOverheadQueryTypedNoop(b *testing.B) {
	db := typedOverheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.Query(func(q *TestQueries) error { return nil })
	}
}

func BenchmarkOverheadQueryTransactionTypedNoop(b *testing.B) {
	db := typedOverheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.QueryTransaction(func(q *TestQueries) error { return nil })
	}
}
