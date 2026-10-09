package database

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/valentin-kaiser/go-core/apperror"
)

// forEachStatement reads an SQL dump line by line and calls fn with every complete statement, in
// order. Blank lines and lines starting with "--" are skipped, and a statement ends at a line that
// ends with a semicolon. Text after the last semicolon is not a statement and is dropped.
//
// Reading line by line keeps the memory use at one statement, where splitting the whole dump
// up front needs the dump several times over, and appending to a string per line is quadratic for
// a long statement.
func forEachStatement(r io.Reader, fn func(stmt string)) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var stmt []byte
	var long []byte // a line longer than the reader's buffer, collected in pieces

	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, line...)
			continue
		}
		if len(long) > 0 {
			long = append(long, line...)
			line = long
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return apperror.Wrap(err)
		}

		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 && !bytes.HasPrefix(trimmed, []byte("--")) {
			stmt = append(stmt, bytes.TrimSuffix(line, []byte("\n"))...)
			stmt = append(stmt, '\n')
			if trimmed[len(trimmed)-1] == ';' {
				fn(string(stmt))
				stmt = stmt[:0]
			}
		}
		long = long[:0]

		if err != nil { // io.EOF
			return nil
		}
	}
}

// copyFile copies a file without reading it into memory
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return apperror.Wrap(err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return apperror.Wrap(err)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return apperror.Wrap(err)
	}
	if err := out.Close(); err != nil {
		return apperror.Wrap(err)
	}
	return nil
}
