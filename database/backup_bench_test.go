package database_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/apperror"
	"github.com/valentin-kaiser/go-core/v2/database"
)

const (
	payloadBytes       = 1000
	createPayloadTable = "CREATE TABLE t (id INTEGER PRIMARY KEY, payload TEXT)"
	insertPayloadRow   = "INSERT INTO t (payload) VALUES (?)"
	benchRows          = 30000
)

// Backup of a file based SQLite database of about 30 MB
func BenchmarkBackupSQLite(b *testing.B) {
	dir := b.TempDir()
	dbPath := filepath.Join(dir, "bench.db")
	db := database.New[TestQueries](database.DriverSQLite, "bench-backup")
	db.Connect(b.Context(), connectInterval, "file:"+dbPath)
	cleanupDisconnect(b, db)
	awaitDatabase(b, db)

	err := db.Execute(b.Context(), func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(createPayloadTable)
		if err != nil {
			return err
		}
		tx, err := sqlDB.Begin()
		if err != nil {
			return err
		}
		payload := make([]byte, payloadBytes)
		for i := range payload {
			payload[i] = 'x'
		}
		for range benchRows {
			_, err := tx.Exec(insertPayloadRow, string(payload))
			if err != nil {
				return apperror.NewError("inserting rows failed").AddError(err).AddError(tx.Rollback())
			}
		}
		return tx.Commit()
	})
	if err != nil {
		b.Fatal(err)
	}

	target := filepath.Join(dir, "backup.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(info.Size())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.Backup(b.Context(), target, "")
		if err != nil {
			b.Fatal(err)
		}
	}
}
