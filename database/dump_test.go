package database

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	benchDumpRows      = 20000
	longLineBytes      = 300000
	longStatementRows  = 5000
	copyPayloadRepeats = 100000
	dumpFileMode       = 0o600
)

// oldStatements is the algorithm Restore used before: split the whole dump and append line by line.
func oldStatements(sqlContent string) []string {
	statements := []string{}
	currentStmt := ""
	for line := range strings.SplitSeq(sqlContent, "\n") {
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
	err := forEachStatement(strings.NewReader(sqlContent), func(s string) error {
		got = append(got, s)
		return nil
	})
	if err != nil {
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
		"long statement":     "INSERT INTO a VALUES\n" + strings.Repeat("(1,'x'),\n", longStatementRows) + "(2,'y');\n",
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
	payload := []byte(strings.Repeat("0123456789", copyPayloadRepeats))
	err := os.WriteFile(src, payload, dumpFileMode)
	if err != nil {
		t.Fatal(err)
	}
	// An existing, longer target is truncated
	err = os.WriteFile(dst, append(payload, payload...), dumpFileMode)
	if err != nil {
		t.Fatal(err)
	}

	err = copyFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Clean(dst))
	if err != nil || string(got) != string(payload) {
		t.Fatalf("copy differs: %d bytes, err=%v", len(got), err)
	}
	err = copyFile(filepath.Join(dir, "missing"), dst)
	if err == nil {
		t.Fatal("copying a missing file did not fail")
	}
	after, err := os.ReadFile(filepath.Clean(dst))
	if err != nil || string(after) != string(payload) {
		t.Fatal("a failed copy destroyed the target")
	}
}

func benchDump() string {
	var b strings.Builder
	b.WriteString("-- MySQL dump\nSET NAMES utf8mb4;\n")
	for range benchDumpRows {
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
		err := forEachStatement(strings.NewReader(dump), func(string) error { n++; return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}

// A line longer than the reader's buffer is read in pieces and must come out whole.
func TestForEachStatementVeryLongLine(t *testing.T) {
	long := "INSERT INTO a VALUES ('" + strings.Repeat("x", longLineBytes) + "');\nSELECT 2;\n"
	want, got := oldStatements(long), newStatements(t, long)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("a long line was read incorrectly: %d statements, lengths %d and %d", len(got), len(want[0]), len(got[0]))
	}
}

func TestAppendMySQLValue(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)
	cases := []struct {
		in   any
		want string
	}{
		{in: nil, want: "NULL"},
		{in: "plain", want: "'plain'"},
		{in: "it's", want: `'it\'s'`},
		{in: "a\\b", want: `'a\\b'`},
		{in: "line\r\nbreak", want: `'line\r\nbreak'`},
		{in: "ctrl\x1az", want: `'ctrl\Zz'`},
		{in: "café", want: "'café'"},
		{in: []byte("bytes"), want: "'bytes'"},
		{in: []byte{0, 1, 255}, want: "X'0001ff'"},
		{in: "nul\x00inside", want: "X'6e756c00696e73696465'"},
		{in: int64(-5), want: "-5"},
		{in: uint64(7), want: "7"},
		{in: 1.5, want: "1.5"},
		{in: true, want: "true"},
		{in: when, want: "'2026-03-04 05:06:07.123456'"},
		{in: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), want: "'2026-03-04 05:06:07'"},
	}
	for _, c := range cases {
		if got := string(appendMySQLValue(nil, c.in)); got != c.want {
			t.Errorf("appendMySQLValue(%#v) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestAppendPostgresValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{in: nil, want: "NULL"},
		{in: "plain", want: "E'plain'"},
		{in: "it's", want: "E'it''s'"},
		{in: "a\\b", want: `E'a\\b'`},
		{in: "line\r\nbreak", want: `E'line\r\nbreak'`},
		{in: []byte{0, 1, 255}, want: `E'\\x0001ff'`},
		{in: "nul\x00inside", want: `E'\\x6e756c00696e73696465'`},
		{in: int64(42), want: "42"},
		{in: 2.25, want: "2.25"},
		{in: false, want: "false"},
		{in: time.Date(2026, 3, 4, 5, 6, 7, 500000000, time.FixedZone("x", 3600)), want: "'2026-03-04 05:06:07.5+01:00'"},
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
		if bytes.ContainsAny(got, "\r\n") {
			t.Errorf("%s: %q holds a raw line break", name, got)
		}
	}
}

func TestRedactDSN(t *testing.T) {
	dsnLogin := "s3cret"
	postgresDSN := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("user", dsnLogin),
		Host:     "host:5432",
		Path:     "/db",
		RawQuery: "sslmode=disable",
	}
	redactedDSN := postgresDSN
	redactedDSN.User = url.UserPassword("user", "xxxxx")
	cases := map[string]string{
		"root:" + dsnLogin + "@tcp(127.0.0.1:3306)/db?parseTime=true": "root:xxxxx@tcp(127.0.0.1:3306)/db?parseTime=true",
		postgresDSN.String():                           redactedDSN.String(),
		"postgres://user@host/db":                      "postgres://user@host/db",
		"file:/data/app.db?_pragma=busy_timeout(5000)": "file:/data/app.db?_pragma=busy_timeout(5000)",
		"": "",
	}
	for in, want := range cases {
		if got := redactDSN(in); got != want {
			t.Errorf("redactDSN(%q) = %q, want %q", in, got, want)
		}
	}
}
