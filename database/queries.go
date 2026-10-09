package database

import (
	"context"
	"database/sql"
	"reflect"

	"github.com/valentin-kaiser/go-core/apperror"
)

// DBTX is the interface a sqlc generated constructor takes. Both *sql.DB and *sql.Tx implement it.
type DBTX interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

// queriesArg is the connection converted to the parameter type of a reflected constructor.
// Passing a value that already has the parameter type spares reflect.Value.Call the
// interface check it otherwise repeats on every call.
type queriesArg struct {
	db    *sql.DB
	param reflect.Type
	value reflect.Value
}

// newQueries builds the Queries value for a connection, which is the *sql.DB or a *sql.Tx.
func (d *Database[Q]) newQueries(conn DBTX) (*Q, error) {
	if d.queriesFunc != nil {
		queries := d.queriesFunc(conn)
		if queries == nil {
			return nil, apperror.NewErrorf("queries constructor returned nil")
		}
		return queries, nil
	}

	fn := reflect.ValueOf(d.queries)
	if fn.Kind() != reflect.Func {
		return nil, apperror.NewErrorf("queries constructor is not a function")
	}
	if fn.Type().NumIn() != 1 {
		return nil, apperror.NewErrorf("queries constructor must take exactly one argument")
	}

	arg := reflect.ValueOf(conn)
	if db, ok := conn.(*sql.DB); ok {
		arg = d.queriesArgFor(db, fn.Type().In(0))
	}

	results := fn.Call([]reflect.Value{arg})
	if len(results) != 1 {
		return nil, apperror.NewErrorf("queries constructor must return exactly one value")
	}

	queries, ok := results[0].Interface().(*Q)
	if !ok {
		return nil, apperror.NewErrorf("queries constructor returned unexpected type")
	}
	return queries, nil
}

// queriesArgFor returns the connection as a value of the constructor's parameter type,
// remembered for as long as the connection stays the same.
func (d *Database[Q]) queriesArgFor(db *sql.DB, param reflect.Type) reflect.Value {
	if cached := d.queriesArg.Load(); cached != nil && cached.db == db && cached.param == param {
		return cached.value
	}

	value := reflect.ValueOf(db)
	if param.Kind() == reflect.Interface && value.Type().Implements(param) {
		converted := reflect.New(param).Elem()
		converted.Set(value)
		value = converted
	}
	d.queriesArg.Store(&queriesArg{db: db, param: param, value: value})
	return value
}
