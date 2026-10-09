package database_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
)

const dumpBenchRows = 20000

// fillDumpTable creates a table of 20000 rows with text, a binary value and a timestamp
func fillDumpTable(b *testing.B, db *database.Database[TestQueries], create string, placeholder func(int) string) {
	b.Helper()
	err := db.Execute(func(c *sql.DB) error {
		for _, s := range []string{"DROP TABLE IF EXISTS dump_bench", create} {
			if _, err := c.Exec(s); err != nil {
				return err
			}
		}
		tx, err := c.Begin()
		if err != nil {
			return err
		}
		stmt, err := tx.Prepare("INSERT INTO dump_bench VALUES (" + placeholder(0) + ")")
		if err != nil {
			return err
		}
		when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
		text := strings.Repeat("some text with 'quotes' and a \\ backslash", 3)
		for i := 0; i < dumpBenchRows; i++ {
			if _, err := stmt.Exec(i, fmt.Sprintf("%s%d", text, i), []byte{1, 2, 3, byte(i)}, float64(i)/7, when); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = db.Execute(func(c *sql.DB) error { _, err := c.Exec("DROP TABLE IF EXISTS dump_bench"); return err })
	})
}

func benchBackupRestore(b *testing.B, db *database.Database[TestQueries], schema string) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "dump.sql")

	b.Run("Backup", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := db.Backup(path, schema); err != nil {
				b.Fatal(err)
			}
		}
		if info, err := os.Stat(path); err == nil {
			b.SetBytes(info.Size())
		}
	})

	b.Run("Restore", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := db.Restore(path); err != nil {
				b.Fatal(err)
			}
		}
		if info, err := os.Stat(path); err == nil {
			b.SetBytes(info.Size())
		}
	})
}

func BenchmarkMySQLBackupRestore(b *testing.B) {
	db := mysqlServer(b)
	fillDumpTable(b, db,
		"CREATE TABLE dump_bench (id BIGINT PRIMARY KEY, txt TEXT, bin BLOB, num DOUBLE, ts DATETIME)",
		func(int) string { return "?, ?, ?, ?, ?" })
	benchBackupRestore(b, db, "")
}

func BenchmarkPostgresBackupRestore(b *testing.B) {
	db := postgresServer(b)
	fillDumpTable(b, db,
		"CREATE TABLE dump_bench (id BIGINT PRIMARY KEY, txt TEXT, bin BYTEA, num DOUBLE PRECISION, ts TIMESTAMPTZ)",
		func(int) string { return "$1, $2, $3, $4, $5" })
	benchBackupRestore(b, db, "public")
}
