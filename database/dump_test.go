package database

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// oldStatements is the algorithm Restore used before: split the whole dump and append line by line.
func oldStatements(sqlContent string) []string {
	statements := []string{}
	currentStmt := ""
	for _, line := range strings.Split(sqlContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		currentStmt += line + "\n"
		if strings.HasSuffix(trimmed, ";") {
			statements = append(statements, currentStmt)
			currentStmt = ""
		}
	}
	return statements
}

func newStatements(t *testing.T, sqlContent string) []string {
	t.Helper()
	got := []string{}
	if err := forEachStatement(strings.NewReader(sqlContent), func(s string) error { got = append(got, s); return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestForEachStatementStopsOnCallbackError(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	err := forEachStatement(strings.NewReader("SELECT 1;\nSELECT 2;\nSELECT 3;\n"), func(string) error {
		calls++
		if calls == 2 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected callback error, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected reading to stop after the failing statement, got %d calls", calls)
	}
}

func TestForEachStatementMatchesOldSplitting(t *testing.T) {
	dumps := map[string]string{
		"simple":             "CREATE TABLE a (id INT);\nINSERT INTO a VALUES (1);\n",
		"comments and blank": "-- header\n\nSET NAMES utf8mb4;\n\n-- table a\nCREATE TABLE a (\n  id INT,\n  name TEXT\n);\n",
		"no final newline":   "SELECT 1;",
		"incomplete tail":    "SELECT 1;\nSELECT 2",
		"crlf":               "CREATE TABLE a (id INT);\r\nINSERT INTO a VALUES (1);\r\n",
		"semicolon in value": "INSERT INTO a VALUES ('x;y');\nINSERT INTO a VALUES ('z');\n",
		"trailing spaces":    "SELECT 1;   \n  SELECT 2;\t\n",
		"comment after code": "SELECT 1;\n-- done\n",
		"empty":              "",
		"only comments":      "-- a\n-- b\n",
		"long statement":     "INSERT INTO a VALUES\n" + strings.Repeat("(1,'x'),\n", 5000) + "(2,'y');\n",
	}
	for name, dump := range dumps {
		t.Run(name, func(t *testing.T) {
			want, got := oldStatements(dump), newStatements(t, dump)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("statements differ\nold: %q\nnew: %q", want, got)
			}
		})
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	dst := filepath.Join(dir, "dst.db")
	payload := []byte(strings.Repeat("0123456789", 100000))
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	// An existing, longer target is truncated
	if err := os.WriteFile(dst, append(payload, payload...), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst, 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("copy differs: %d bytes, err=%v", len(got), err)
	}
	if err := copyFile(filepath.Join(dir, "missing"), dst, 0o640); err == nil {
		t.Fatal("copying a missing file did not fail")
	}
	if after, _ := os.ReadFile(dst); string(after) != string(payload) {
		t.Fatal("a failed copy destroyed the target")
	}
}

func benchDump() string {
	var b strings.Builder
	b.WriteString("-- MySQL dump\nSET NAMES utf8mb4;\n")
	for i := 0; i < 20000; i++ {
		b.WriteString("INSERT INTO `users` VALUES (1,'name','2026-01-01 00:00:00','some text that makes the row longer');\n")
	}
	return b.String()
}

func BenchmarkSplitDumpOld(b *testing.B) {
	dump := benchDump()
	b.SetBytes(int64(len(dump)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = oldStatements(dump)
	}
}

func BenchmarkForEachStatement(b *testing.B) {
	dump := benchDump()
	b.SetBytes(int64(len(dump)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		n := 0
		_ = forEachStatement(strings.NewReader(dump), func(string) error { n++; return nil })
	}
}

// A line longer than the reader's buffer is read in pieces and must come out whole.
func TestForEachStatementVeryLongLine(t *testing.T) {
	long := "INSERT INTO a VALUES ('" + strings.Repeat("x", 300000) + "');\nSELECT 2;\n"
	want, got := oldStatements(long), newStatements(t, long)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("a long line was read incorrectly: %d statements, lengths %d and %d", len(got), len(want[0]), len(got[0]))
	}
}

func TestAppendMySQLValue(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)
	cases := []struct {
		in   interface{}
		want string
	}{
		{nil, "NULL"},
		{"plain", "'plain'"},
		{"it's", `'it\'s'`},
		{"a\\b", `'a\\b'`},
		{"line\r\nbreak", `'line\r\nbreak'`},
		{"ctrl\x1az", `'ctrl\Zz'`},
		{"café", "'café'"},
		{[]byte("bytes"), "'bytes'"},
		{[]byte{0, 1, 255}, "X'0001ff'"},
		{"nul\x00inside", "X'6e756c00696e73696465'"},
		{int64(-5), "-5"},
		{uint64(7), "7"},
		{1.5, "1.5"},
		{true, "true"},
		{when, "'2026-03-04 05:06:07.123456'"},
		{time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), "'2026-03-04 05:06:07'"},
	}
	for _, c := range cases {
		if got := string(appendMySQLValue(nil, c.in)); got != c.want {
			t.Errorf("appendMySQLValue(%#v) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestAppendPostgresValue(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{nil, "NULL"},
		{"plain", "E'plain'"},
		{"it's", "E'it''s'"},
		{"a\\b", `E'a\\b'`},
		{"line\r\nbreak", `E'line\r\nbreak'`},
		{[]byte{0, 1, 255}, `E'\\x0001ff'`},
		{"nul\x00inside", `E'\\x6e756c00696e73696465'`},
		{int64(42), "42"},
		{2.25, "2.25"},
		{false, "false"},
		{time.Date(2026, 3, 4, 5, 6, 7, 500000000, time.FixedZone("x", 3600)), "'2026-03-04 05:06:07.5+01:00'"},
	}
	for _, c := range cases {
		if got := string(appendPostgresValue(nil, c.in)); got != c.want {
			t.Errorf("appendPostgresValue(%#v) = %s, want %s", c.in, got, c.want)
		}
	}
}

// A dump row never contains a raw newline, so the line based reader can not split a value.
func TestDumpValuesStayOnOneLine(t *testing.T) {
	nasty := "a\nb\r\nc;\n-- not a comment\n'"
	for name, got := range map[string][]byte{
		"mysql":    appendMySQLValue(nil, nasty),
		"postgres": appendPostgresValue(nil, nasty),
	} {
		if strings.ContainsAny(string(got), "\r\n") {
			t.Errorf("%s: %q holds a raw line break", name, got)
		}
	}
}

func TestRedactDSN(t *testing.T) {
	cases := map[string]string{
		"root:secret@tcp(127.0.0.1:3306)/db?parseTime=true":   "root:xxxxx@tcp(127.0.0.1:3306)/db?parseTime=true",
		"postgres://user:secret@host:5432/db?sslmode=disable": "postgres://user:xxxxx@host:5432/db?sslmode=disable",
		"postgres://user@host/db":                             "postgres://user@host/db",
		"file:/data/app.db?_pragma=busy_timeout(5000)":        "file:/data/app.db?_pragma=busy_timeout(5000)",
		"": "",
	}
	for in, want := range cases {
		if got := redactDSN(in); got != want {
			t.Errorf("redactDSN(%q) = %q, want %q", in, got, want)
		}
	}
}
