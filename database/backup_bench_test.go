package database_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
)

// Backup of a file based SQLite database of about 30 MB
func BenchmarkBackupSQLite(b *testing.B) {
	dir := b.TempDir()
	dbPath := filepath.Join(dir, "bench.db")
	db := database.New[TestQueries](database.DriverSQLite, "bench-backup")
	db.Connect(100*time.Millisecond, "file:"+dbPath)
	b.Cleanup(func() { _ = db.Disconnect() })
	db.AwaitConnection()

	err := db.Execute(func(sqlDB *sql.DB) error {
		if _, err := sqlDB.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, payload TEXT)"); err != nil {
			return err
		}
		tx, err := sqlDB.Begin()
		if err != nil {
			return err
		}
		payload := make([]byte, 1000)
		for i := range payload {
			payload[i] = 'x'
		}
		for i := 0; i < 30000; i++ {
			if _, err := tx.Exec("INSERT INTO t (payload) VALUES (?)", string(payload)); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		b.Fatal(err)
	}

	target := filepath.Join(dir, "backup.db")
	info, _ := os.Stat(dbPath)
	b.SetBytes(info.Size())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Backup(target, ""); err != nil {
			b.Fatal(err)
		}
	}
}
