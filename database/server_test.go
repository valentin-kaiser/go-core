package database_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
)

// These tests need a MySQL and a PostgreSQL server and skip without them. The defaults match
// test/compose.yaml; MYSQL_DSN and POSTGRES_DSN override them.

func serverDSN(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fallback
}

func connectServer(t testing.TB, driver database.Driver, name, dsn string) *database.Database[TestQueries] {
	t.Helper()
	db := database.New[TestQueries](driver, name)
	db.Connect(100*time.Millisecond, dsn)
	t.Cleanup(func() { _ = db.Disconnect() })

	done := make(chan struct{})
	go func() { db.AwaitConnection(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Skipf("%s server not available at %s", name, dsn)
	}
	return db
}

func mysqlServer(t testing.TB) *database.Database[TestQueries] {
	return connectServer(t, database.DriverMySQL, "mysql",
		serverDSN("MYSQL_DSN", "root:bench@tcp(127.0.0.1:13306)/bench?parseTime=true&multiStatements=false"))
}

func postgresServer(t testing.TB) *database.Database[TestQueries] {
	return connectServer(t, database.DriverPostgres, "postgres",
		serverDSN("POSTGRES_DSN", "postgres://postgres:bench@127.0.0.1:15432/bench?sslmode=disable"))
}

var trickyStrings = []string{
	"plain",
	"it's a \"test\"\nnew line\r\nand \\ backslash; -- not a comment;",
	"héllo ✓ 日本語",
	"ends with a semicolon;",
	"-- looks like a comment",
	"",
	"back\\nslash n",
	"tab\tand 'quotes' and ''double''",
}

func execAll(t testing.TB, db *database.Database[TestQueries], statements ...string) {
	t.Helper()
	err := db.Execute(func(c *sql.DB) error {
		for _, s := range statements {
			if _, err := c.Exec(s); err != nil {
				return fmt.Errorf("%s: %w", s, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type dumpRow struct {
	ID   int64
	Text sql.NullString
	Blob []byte
	Num  sql.NullFloat64
	When sql.NullTime
	Flag sql.NullBool
}

func readRows(t testing.TB, db *database.Database[TestQueries], query string) []dumpRow {
	t.Helper()
	var out []dumpRow
	err := db.Execute(func(c *sql.DB) error {
		rows, err := c.Query(query)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var r dumpRow
			if err := rows.Scan(&r.ID, &r.Text, &r.Blob, &r.Num, &r.When, &r.Flag); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func compareRows(t *testing.T, want, got []dumpRow) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%d rows after the restore, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if w.ID != g.ID || w.Text != g.Text || !bytes.Equal(w.Blob, g.Blob) || w.Num != g.Num || w.Flag != g.Flag ||
			!w.When.Time.Equal(g.When.Time) || w.When.Valid != g.When.Valid {
			t.Errorf("row %d differs\nbefore: %+v\nafter:  %+v", w.ID, w, g)
		}
	}
}

func TestMySQLBackupRestoreRoundTrip(t *testing.T) {
	db := mysqlServer(t)
	execAll(t, db,
		"DROP TABLE IF EXISTS dump_roundtrip",
		"CREATE TABLE dump_roundtrip (id BIGINT PRIMARY KEY, txt TEXT NULL, bin BLOB NULL, num DOUBLE NULL, ts DATETIME(6) NULL, flag TINYINT(1) NULL)",
	)
	t.Cleanup(func() { execAll(t, db, "DROP TABLE IF EXISTS dump_roundtrip") })

	when := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)
	err := db.Execute(func(c *sql.DB) error {
		for i, s := range trickyStrings {
			if _, err := c.Exec("INSERT INTO dump_roundtrip VALUES (?, ?, ?, ?, ?, ?)", i+1, s, []byte{0, 1, 2, 255, 254, byte(i)}, float64(i)+0.1, when, i%2); err != nil {
				return err
			}
		}
		_, err := c.Exec("INSERT INTO dump_roundtrip VALUES (100, NULL, NULL, NULL, NULL, NULL)")
		if err != nil {
			return err
		}
		// More rows than fit in one INSERT statement of the dump
		for i := 0; i < 1300; i++ {
			if _, err := c.Exec("INSERT INTO dump_roundtrip VALUES (?, ?, NULL, ?, ?, 1)", 1000+i, fmt.Sprintf("row %d; 'x'", i), float64(i)/3, when); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	query := "SELECT id, txt, bin, num, ts, flag FROM dump_roundtrip ORDER BY id"
	want := readRows(t, db, query)

	backup := filepath.Join(t.TempDir(), "mysql.sql")
	if err := db.Backup(backup, ""); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	dump, _ := os.ReadFile(backup)
	if strings.Contains(string(dump), "bench@") || strings.Contains(string(dump), ":bench") {
		t.Error("the backup contains the password of the DSN")
	}
	if inserts := strings.Count(string(dump), "INSERT INTO `dump_roundtrip`"); inserts < 3 {
		t.Errorf("%d INSERT statements; the rows should be written in batches", inserts)
	}

	execAll(t, db, "DELETE FROM dump_roundtrip")
	if err := db.Restore(backup); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	compareRows(t, want, readRows(t, db, query))
}

func TestPostgresBackupRestoreRoundTrip(t *testing.T) {
	db := postgresServer(t)
	execAll(t, db,
		"DROP TABLE IF EXISTS dump_roundtrip",
		"CREATE TABLE dump_roundtrip (id BIGINT PRIMARY KEY, txt TEXT NULL, bin BYTEA NULL, num DOUBLE PRECISION NULL, ts TIMESTAMPTZ NULL, flag BOOLEAN NULL)",
	)
	t.Cleanup(func() { execAll(t, db, "DROP TABLE IF EXISTS dump_roundtrip") })

	when := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)
	err := db.Execute(func(c *sql.DB) error {
		for i, s := range trickyStrings {
			if _, err := c.Exec("INSERT INTO dump_roundtrip VALUES ($1, $2, $3, $4, $5, $6)", i+1, s, []byte{0, 1, 2, 255, 254, byte(i)}, float64(i)+0.1, when, i%2 == 0); err != nil {
				return err
			}
		}
		if _, err := c.Exec("INSERT INTO dump_roundtrip VALUES (100, NULL, NULL, NULL, NULL, NULL)"); err != nil {
			return err
		}
		for i := 0; i < 1300; i++ {
			if _, err := c.Exec("INSERT INTO dump_roundtrip VALUES ($1, $2, NULL, $3, $4, true)", 1000+i, fmt.Sprintf("row %d; 'x'", i), float64(i)/3, when); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	query := "SELECT id, txt, bin, num, ts, flag FROM dump_roundtrip ORDER BY id"
	want := readRows(t, db, query)

	backup := filepath.Join(t.TempDir(), "postgres.sql")
	if err := db.Backup(backup, "public"); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	dump, _ := os.ReadFile(backup)
	if strings.Contains(string(dump), ":bench@") {
		t.Error("the backup contains the password of the DSN")
	}

	execAll(t, db, "DELETE FROM dump_roundtrip")
	if err := db.Restore(backup); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	compareRows(t, want, readRows(t, db, query))
}

// The dump switches foreign key checks off and on again; that only works when every statement
// of the restore runs on the same connection. The child table sorts, and so is restored,
// before the table it refers to.
func TestMySQLRestoreWithForeignKeys(t *testing.T) {
	db := mysqlServer(t)
	execAll(t, db,
		"DROP TABLE IF EXISTS a_fk_child",
		"DROP TABLE IF EXISTS b_fk_parent",
		"CREATE TABLE b_fk_parent (id INT PRIMARY KEY, name VARCHAR(20))",
		"CREATE TABLE a_fk_child (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES b_fk_parent (id))",
		"INSERT INTO b_fk_parent VALUES (1, 'one'), (2, 'two')",
		"INSERT INTO a_fk_child VALUES (10, 1), (11, 2)",
	)
	t.Cleanup(func() {
		execAll(t, db, "SET FOREIGN_KEY_CHECKS = 0", "DROP TABLE IF EXISTS a_fk_child", "DROP TABLE IF EXISTS b_fk_parent")
	})

	backup := filepath.Join(t.TempDir(), "fk.sql")
	if err := db.Backup(backup, ""); err != nil {
		t.Fatal(err)
	}

	// Remove both tables, the way a restore into an empty database starts
	err := db.Execute(func(c *sql.DB) error {
		conn, err := c.Conn(t.Context())
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		for _, s := range []string{"SET FOREIGN_KEY_CHECKS = 0", "DROP TABLE a_fk_child", "DROP TABLE b_fk_parent", "SET FOREIGN_KEY_CHECKS = 1"} {
			if _, err := conn.ExecContext(t.Context(), s); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Restore(backup); err != nil {
		t.Fatal(err)
	}

	var children, parents int
	err = db.Execute(func(c *sql.DB) error {
		if err := c.QueryRow("SELECT COUNT(*) FROM a_fk_child").Scan(&children); err != nil {
			return err
		}
		return c.QueryRow("SELECT COUNT(*) FROM b_fk_parent").Scan(&parents)
	})
	if err != nil {
		t.Fatal(err)
	}
	if children != 2 || parents != 2 {
		t.Fatalf("%d children and %d parents after the restore, want 2 and 2", children, parents)
	}
}
