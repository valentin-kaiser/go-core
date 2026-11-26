package database_test

import (
	"database/sql"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/database"
)

// These benchmarks isolate what database.Query / Transaction add around a
// statement: the callback does no SQL, so what is measured is the wrapper
// (RLock, connected check, recover defer, reflective query-set construction).
// The raw baseline is a direct call of the same constructor.

func overheadDB(b *testing.B) *database.Connection[TestQueries] {
	b.Helper()
	db := database.New[TestQueries](database.DriverSQLite, "overhead")
	db.RegisterQueries(NewTestQueries)
	db.Connect(connectInterval, memoryDSN)
	cleanupDisconnect(b, db)
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
		err := db.Query(func(_ *TestQueries) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOverheadQueryNoopParallel(b *testing.B) {
	db := overheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			err := db.Query(func(_ *TestQueries) error { return nil })
			if err != nil {
				b.Error(err)
			}
		}
	})
}

func BenchmarkOverheadTransactionNoop(b *testing.B) {
	db := overheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.Transaction(func(_ *sql.Tx) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOverheadQueryTransactionNoop(b *testing.B) {
	db := overheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.QueryTransaction(func(_ *TestQueries) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}

func typedOverheadDB(b *testing.B) *database.Connection[TestQueries] {
	b.Helper()
	db := database.New[TestQueries](database.DriverSQLite, "overhead-typed")
	db.RegisterQueriesFunc(func(c database.DBTX) *TestQueries { return NewTestQueries(c) })
	db.Connect(connectInterval, memoryDSN)
	cleanupDisconnect(b, db)
	db.AwaitConnection()
	return db
}

func BenchmarkOverheadQueryTypedNoop(b *testing.B) {
	db := typedOverheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.Query(func(_ *TestQueries) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOverheadQueryTransactionTypedNoop(b *testing.B) {
	db := typedOverheadDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.QueryTransaction(func(_ *TestQueries) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}
