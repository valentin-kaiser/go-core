package database

import (
	"bufio"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/valentin-kaiser/go-core/v2/apperror"
)

// Rows of a table are written as multi-row INSERT statements. A statement is closed after this
// many rows or bytes, so a restore never has to send one statement as large as a table (the
// server limits the size of a statement) and the dump reader never holds more than one batch.
const (
	dumpBatchRows  = 500
	dumpBatchBytes = 1 << 20

	redactedPassword   = "xxxxx"
	mysqlTimeLayout    = "2006-01-02 15:04:05.999999"
	postgresTimeLayout = "2006-01-02 15:04:05.999999999-07:00"

	decimalBase       = 10
	floatFormat       = 'g'
	shortestPrecision = -1
	float64BitSize    = 64
	writeBufferSize   = 256 * 1024

	nullLiteral          = "NULL"
	insertInto           = "INSERT INTO "
	selectAllFrom        = "SELECT * FROM " //nolint:unqueryvet // a dump needs every column
	valuesKeyword        = " VALUES"
	mysqlStatementEnd    = ";"
	postgresStatementEnd = " ON CONFLICT DO NOTHING;"
	mysqlShowTables      = "SHOW TABLES"
	mysqlDumpHeader      = "SET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS = 0;\n\n"
	mysqlDumpFooter      = "SET FOREIGN_KEY_CHECKS = 1;\n"
)

// redactDSN hides the password of a DSN so it does not end up in a backup file
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err == nil && u.Scheme != "" && u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), redactedPassword)
			return u.String()
		}
		return dsn
	}
	// user:password@tcp(host:port)/name, the form of the MySQL driver
	if at := strings.LastIndex(dsn, "@"); at > 0 {
		if colon := strings.Index(dsn[:at], ":"); colon >= 0 {
			return dsn[:colon+1] + redactedPassword + dsn[at:]
		}
	}
	return dsn
}

// appendMySQLString appends a quoted string literal. Text is escaped the way MySQL reads it
// with backslash escapes on; bytes that are not valid text become a hex literal, which any
// column type takes.
func appendMySQLString(dst []byte, s string) []byte {
	if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
		dst = append(dst, "X'"...)
		dst = hex.AppendEncode(dst, []byte(s))
		return append(dst, '\'')
	}
	dst = append(dst, '\'')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\'':
			dst = append(dst, '\\', '\'')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case 0x1a:
			dst = append(dst, '\\', 'Z')
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, '\'')
}

// appendMySQLValue appends a value scanned from a MySQL result as a SQL literal
func appendMySQLValue(dst []byte, val any) []byte {
	switch v := val.(type) {
	case nil:
		return append(dst, nullLiteral...)
	case []byte:
		return appendMySQLString(dst, string(v))
	case string:
		return appendMySQLString(dst, v)
	case time.Time:
		dst = append(dst, '\'')
		dst = v.AppendFormat(dst, mysqlTimeLayout)
		return append(dst, '\'')
	case int64:
		return strconv.AppendInt(dst, v, decimalBase)
	case uint64:
		return strconv.AppendUint(dst, v, decimalBase)
	case float64:
		return strconv.AppendFloat(dst, v, floatFormat, shortestPrecision, float64BitSize)
	case bool:
		return strconv.AppendBool(dst, v)
	default:
		return appendMySQLString(dst, fmt.Sprint(v))
	}
}

// appendPostgresString appends a string literal with the E prefix. Backslashes mean the same in such
// a literal whatever standard_conforming_strings is set to, which a plain literal does not guarantee,
// and newlines are escaped so every row stays on one line.
func appendPostgresString(dst []byte, s string) []byte {
	// A NUL byte can not be stored in text; invalid UTF-8 is rejected by the server as well
	if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
		return appendPostgresBytea(dst, []byte(s))
	}
	dst = append(dst, "E'"...)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\'':
			dst = append(dst, '\'', '\'')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, '\'')
}

// appendPostgresBytea appends bytes in the hex input form of bytea
func appendPostgresBytea(dst []byte, b []byte) []byte {
	dst = append(dst, "E'\\\\x"...)
	dst = hex.AppendEncode(dst, b)
	return append(dst, '\'')
}

// appendPostgresValue appends a value scanned from a PostgreSQL result as a SQL literal
func appendPostgresValue(dst []byte, val any) []byte {
	switch v := val.(type) {
	case nil:
		return append(dst, nullLiteral...)
	case []byte:
		return appendPostgresBytea(dst, v)
	case string:
		return appendPostgresString(dst, v)
	case time.Time:
		dst = append(dst, '\'')
		dst = v.AppendFormat(dst, postgresTimeLayout)
		return append(dst, '\'')
	case int64:
		return strconv.AppendInt(dst, v, decimalBase)
	case float64:
		return strconv.AppendFloat(dst, v, floatFormat, shortestPrecision, float64BitSize)
	case bool:
		return strconv.AppendBool(dst, v)
	default:
		return appendPostgresString(dst, fmt.Sprint(v))
	}
}

// dumpTable writes the rows as batched INSERT statements. prefix is the "INSERT INTO ... VALUES"
// text without the newline, suffix what follows the last row of a statement, including the semicolon.
func dumpTable(w *bufio.Writer, rows *sql.Rows, columns int, prefix, suffix string, appendValue func([]byte, any) []byte) error {
	values := make([]any, columns)
	pointers := make([]any, columns)
	for i := range values {
		pointers[i] = &values[i]
	}

	var row []byte
	inBatch, batchBytes := 0, 0
	for rows.Next() {
		err := rows.Scan(pointers...)
		if err != nil {
			return apperror.NewError("scanning row failed").AddError(err)
		}
		row = append(row[:0], '(')
		for i, val := range values {
			if i > 0 {
				row = append(row, ',', ' ')
			}
			row = appendValue(row, val)
		}
		row = append(row, ')')
		switch inBatch {
		case 0:
			_, err = w.WriteString(prefix)
			if err == nil {
				err = w.WriteByte('\n')
			}
		default:
			_, err = w.WriteString(",\n")
		}
		if err == nil {
			_, err = w.Write(row)
		}
		if err != nil {
			return apperror.NewError("writing values failed").AddError(err)
		}
		inBatch++
		batchBytes += len(row)
		if inBatch >= dumpBatchRows || batchBytes >= dumpBatchBytes {
			_, err := w.WriteString(suffix + "\n")
			if err != nil {
				return apperror.NewError("writing statement terminator failed").AddError(err)
			}
			inBatch, batchBytes = 0, 0
		}
	}
	err := rows.Err()
	if err != nil {
		return apperror.NewError("reading rows failed").AddError(err)
	}

	if inBatch > 0 {
		_, err := w.WriteString(suffix + "\n")
		if err != nil {
			return apperror.NewError("writing statement terminator failed").AddError(err)
		}
	}
	return nil
}

// quotedColumns quotes the column names for an INSERT
func quotedColumns(columns []string, driver Driver) (string, error) {
	quoted := make([]string, len(columns))
	for i, col := range columns {
		q, err := quoteIdentifier(col, driver)
		if err != nil {
			return "", apperror.NewError("quoting column name failed").AddError(err)
		}
		quoted[i] = q
	}
	return strings.Join(quoted, ", "), nil
}

// backupMySQL writes an SQL dump of the whole MySQL/MariaDB database
func (d *Connection[Q]) backupMySQL(dbInstance *sql.DB, path string) (err error) {
	file, err := os.Create(filepath.Clean(path))
	if err != nil {
		return apperror.NewError("creating backup file failed").AddError(err)
	}
	// Writes are buffered: a dump has one small write per row otherwise
	w := bufio.NewWriterSize(file, writeBufferSize)
	defer func() {
		flushErr := w.Flush()
		if err == nil && flushErr != nil {
			err = apperror.NewError("writing backup file failed").AddError(flushErr)
		}
		closeErr := file.Close()
		if err == nil && closeErr != nil {
			err = apperror.NewError("closing backup file failed").AddError(closeErr)
		}
	}()

	_, err = fmt.Fprintf(w, "-- MySQL/MariaDB database backup\n-- DSN: %s\n-- Generated: %s\n\n",
		redactDSN(d.getDSN()), time.Now().Format(time.RFC3339))
	if err != nil {
		return apperror.NewError("writing backup header failed").AddError(err)
	}
	_, err = w.WriteString("SET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS = 0;\n\n")
	if err != nil {
		return apperror.NewError("writing charset settings failed").AddError(err)
	}

	rows, err := dbInstance.Query(mysqlShowTables)
	if err != nil {
		return apperror.NewError("listing tables failed").AddError(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		err := rows.Scan(&table)
		if err != nil {
			apperror.Catch(rows.Close, "closing table list rows failed")
			return apperror.NewError("scanning table name failed").AddError(err)
		}
		tables = append(tables, table)
	}
	apperror.Catch(rows.Close, "closing table list rows failed")

	for _, table := range tables {
		quotedTable, err := quoteIdentifier(table, DriverMySQL)
		if err != nil {
			d.logger.Warn().Err(err).Field("table", table).Msg("validating table name failed")
			continue
		}
		var tableName, createStmt string
		err = dbInstance.QueryRow("SHOW CREATE TABLE "+quotedTable).Scan(&tableName, &createStmt)
		if err != nil {
			d.logger.Warn().Err(err).Field("table", table).Msg("getting table schema failed")
			continue
		}
		_, err = fmt.Fprintf(w, "\n-- Table structure for %s\nDROP TABLE IF EXISTS %s;\n%s;\n\n", table, quotedTable, createStmt)
		if err != nil {
			return apperror.NewError("writing table schema failed").AddError(err)
		}
		dataRows, err := dbInstance.Query(selectAllFrom + quotedTable) //nolint:gosec,unqueryvet // quotedTable is escaped by quoteIdentifier; a dump needs every column
		if err != nil {
			d.logger.Warn().Err(err).Field("table", table).Msg("reading table data failed")
			continue
		}
		columns, err := dataRows.Columns()
		if err != nil {
			apperror.Catch(dataRows.Close, "closing table data rows failed")
			return apperror.NewErrorf("getting columns of table %s failed", table).AddError(err)
		}
		if len(columns) > 0 {
			cols, err := quotedColumns(columns, DriverMySQL)
			if err != nil {
				apperror.Catch(dataRows.Close, "closing table data rows failed")
				return apperror.NewErrorf("quoting columns of table %s failed", table).AddError(err)
			}
			_, err = fmt.Fprintf(w, "-- Data for table %s\n", table)
			if err != nil {
				apperror.Catch(dataRows.Close, "closing table data rows failed")
				return apperror.NewError("writing data header failed").AddError(err)
			}
			prefix := insertInto + quotedTable + " (" + cols + ")" + valuesKeyword
			err = dumpTable(w, dataRows, len(columns), prefix, mysqlStatementEnd, appendMySQLValue)
			if err != nil {
				apperror.Catch(dataRows.Close, "closing table data rows failed")
				return apperror.NewErrorf("dumping data of table %s failed", table).AddError(err)
			}
			_, err = w.WriteString("\n")
			if err != nil {
				apperror.Catch(dataRows.Close, "closing table data rows failed")
				return apperror.NewError("writing newline failed").AddError(err)
			}
		}
		apperror.Catch(dataRows.Close, "closing table data rows failed")
	}

	_, err = w.WriteString("SET FOREIGN_KEY_CHECKS = 1;\n")
	if err != nil {
		return apperror.NewError("writing footer failed").AddError(err)
	}

	d.logger.Info().Field("path", path).Msg("database backup created")
	return nil
}

// backupPostgres writes an SQL dump of the tables of a schema of a PostgreSQL database
func (d *Connection[Q]) backupPostgres(dbInstance *sql.DB, path string, schema string) (err error) {
	file, err := os.Create(filepath.Clean(path))
	if err != nil {
		return apperror.NewError("creating backup file failed").AddError(err)
	}
	w := bufio.NewWriterSize(file, writeBufferSize)
	defer func() {
		flushErr := w.Flush()
		if err == nil && flushErr != nil {
			err = apperror.NewError("writing backup file failed").AddError(flushErr)
		}
		closeErr := file.Close()
		if err == nil && closeErr != nil {
			err = apperror.NewError("closing backup file failed").AddError(closeErr)
		}
	}()

	_, err = fmt.Fprintf(w, "-- PostgreSQL database backup\n-- DSN: %s\n-- Generated: %s\n\n",
		redactDSN(d.getDSN()), time.Now().Format(time.RFC3339))
	if err != nil {
		return apperror.NewError("writing backup header failed").AddError(err)
	}

	quotedSchema, err := quoteIdentifier(schema, DriverPostgres)
	if err != nil {
		return apperror.NewError("quoting schema name failed").AddError(err)
	}

	rows, err := dbInstance.Query(`
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = $1
		ORDER BY tablename
	`, schema)
	if err != nil {
		return apperror.NewError("listing tables failed").AddError(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		err := rows.Scan(&table)
		if err != nil {
			apperror.Catch(rows.Close, "closing table list rows failed")
			return apperror.NewError("scanning table name failed").AddError(err)
		}
		tables = append(tables, table)
	}
	apperror.Catch(rows.Close, "closing table list rows failed")

	for _, table := range tables {
		var createStmt string
		err = dbInstance.QueryRow(`
			SELECT 'CREATE TABLE IF NOT EXISTS "' || c.relname || '" (' ||
				string_agg(a.attname || ' ' || pg_catalog.format_type(a.atttypid, a.atttypmod) ||
					CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE '' END, ', ') ||
				');' as create_stmt
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_attribute a ON a.attrelid = c.oid
			WHERE c.relname = $1 AND n.nspname = $2 AND a.attnum > 0 AND NOT a.attisdropped
			GROUP BY c.relname
		`, table, schema).Scan(&createStmt)
		if err != nil {
			d.logger.Warn().Err(err).Field("table", table).Msg("getting table schema failed")
			continue
		}
		_, err = fmt.Fprintf(w, "\n-- Table: %s\n%s\n\n", table, createStmt)
		if err != nil {
			return apperror.NewError("writing table schema failed").AddError(err)
		}
		quotedTable, err := quoteIdentifier(table, DriverPostgres)
		if err != nil {
			return apperror.NewError("quoting table name failed").AddError(err)
		}
		dataRows, err := dbInstance.Query(selectAllFrom + quotedSchema + "." + quotedTable) //nolint:gosec,unqueryvet // both identifiers are escaped by quoteIdentifier; a dump needs every column
		if err != nil {
			d.logger.Warn().Err(err).Field("table", table).Msg("reading table data failed")
			continue
		}
		columns, err := dataRows.Columns()
		if err != nil {
			apperror.Catch(dataRows.Close, "closing table data rows failed")
			return apperror.NewErrorf("getting columns of table %s failed", table).AddError(err)
		}
		if len(columns) > 0 {
			cols, err := quotedColumns(columns, DriverPostgres)
			if err != nil {
				apperror.Catch(dataRows.Close, "closing table data rows failed")
				return apperror.NewErrorf("quoting columns of table %s failed", table).AddError(err)
			}
			_, err = fmt.Fprintf(w, "-- Data for table: %s\n", table)
			if err != nil {
				apperror.Catch(dataRows.Close, "closing table data rows failed")
				return apperror.NewError("writing data header failed").AddError(err)
			}
			prefix := insertInto + quotedSchema + "." + quotedTable + " (" + cols + ")" + valuesKeyword
			// Rows that exist already are kept: one duplicate would otherwise abort the whole restore
			err = dumpTable(w, dataRows, len(columns), prefix, postgresStatementEnd, appendPostgresValue)
			if err != nil {
				apperror.Catch(dataRows.Close, "closing table data rows failed")
				return apperror.NewErrorf("dumping data of table %s failed", table).AddError(err)
			}
		}
		apperror.Catch(dataRows.Close, "closing table data rows failed")
		_, err = w.WriteString("\n")
		if err != nil {
			return apperror.NewError("writing newline failed").AddError(err)
		}
	}

	d.logger.Info().Field("path", path).Msg("database backup created")
	return nil
}
