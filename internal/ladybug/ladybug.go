// Package ladybug is kartograph's binding of LadybugDB: cgo over the C API of the pinned static library (ADR-0007)
// and one C++ call the C API lacks. fetch.sh puts the library and its headers into lib/.
package ladybug

/*
#cgo CFLAGS: -I${SRCDIR}/lib -DLBUG_STATIC_DEFINE
#cgo CXXFLAGS: -std=c++20 -I${SRCDIR}/lib -DLBUG_STATIC_DEFINE
#cgo linux LDFLAGS: ${SRCDIR}/lib/liblbug.a -lstdc++ -lm -ldl -lpthread -lssl -lcrypto
#include <stdlib.h>
#include "lbug.h"
int kg_statement_type(void* prepared_statement);
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Version is the pinned LadybugDB release; fetch.sh pins the same one.
const Version = "0.21.2"

// statementQuery is lbug::common::StatementType::QUERY (src/include/common/enums/statement_type.h at v0.21.2).
const statementQuery = 0

// Config is the part of the engine's system config that kartograph sets; the rest keeps the engine's values.
type Config struct {
	// BufferPool is the buffer pool size in bytes.
	BufferPool uint64
	Threads    uint64
	// MaxSize is the size in bytes the database file may grow to; the engine reserves that much address space.
	MaxSize  uint64
	ReadOnly bool
}

// DB is one open database file with one connection.
type DB struct {
	db   C.lbug_database
	conn C.lbug_connection
}

// Rejected is a statement that the engine or Query refused: it did not parse or bind, it writes, it is not a query,
// or it failed while running.
type Rejected struct {
	Reason string
}

func (r *Rejected) Error() string {
	return r.Reason
}

// Field is one named value of a node, relationship or struct, in the engine's order.
type Field struct {
	Name  string
	Value any
}

// Result holds the columns and rows of a query. A value is nil, bool, int64, uint64, float64, string, []any or
// []Field; a type without a Go counterpart comes as the engine's text of the value.
type Result struct {
	Columns []string
	Rows    [][]any
}

func Open(path string, cfg Config) (*DB, error) {
	// the engine's default pool takes 80% of RAM; every caller sets its own
	if cfg.BufferPool == 0 {
		return nil, fmt.Errorf("open the graph %s: no buffer pool size", path)
	}
	config := C.lbug_default_system_config()
	config.buffer_pool_size = C.uint64_t(cfg.BufferPool)
	config.max_num_threads = C.uint64_t(cfg.Threads)
	config.max_db_size = C.uint64_t(cfg.MaxSize)
	config.read_only = C.bool(cfg.ReadOnly)
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	d := &DB{}
	if C.lbug_database_init(cpath, config, &d.db) != C.LbugSuccess {
		return nil, fmt.Errorf("open the graph %s: %s", path, lastError())
	}
	if C.lbug_connection_init(&d.db, &d.conn) != C.LbugSuccess {
		C.lbug_database_destroy(&d.db)
		return nil, fmt.Errorf("connect to the graph %s: %s", path, lastError())
	}
	return d, nil
}

// Close closes the connection and the database; a database opened for writing checkpoints on close.
func (d *DB) Close() {
	C.lbug_connection_destroy(&d.conn)
	C.lbug_database_destroy(&d.db)
}

// Exec runs one statement of any type and drops its rows.
func (d *DB) Exec(statement string) error {
	return d.run(statement, false, func(*C.lbug_query_result) error { return nil })
}

// Query runs one statement that the engine parses as a query and returns its rows. Any other statement type is
// rejected before it runs, because the read-only mode of the engine lets COPY TO, EXPORT DATABASE, INSTALL, ATTACH,
// option calls and CHECKPOINT write outside the database file or change the process.
func (d *DB) Query(statement string) (*Result, error) {
	var result *Result
	err := d.run(statement, true, func(r *C.lbug_query_result) error {
		var err error
		result, err = rows(r)
		return err
	})
	return result, err
}

func (d *DB) run(statement string, queryOnly bool, read func(*C.lbug_query_result) error) error {
	cstatement := C.CString(statement)
	defer C.free(unsafe.Pointer(cstatement))
	var prepared C.lbug_prepared_statement
	if C.lbug_connection_prepare(&d.conn, cstatement, &prepared) != C.LbugSuccess {
		return &Rejected{Reason: lastError()}
	}
	defer C.lbug_prepared_statement_destroy(&prepared)
	if !C.lbug_prepared_statement_is_success(&prepared) {
		return &Rejected{Reason: takeString(C.lbug_prepared_statement_get_error_message(&prepared))}
	}
	if queryOnly && C.kg_statement_type(prepared._prepared_statement) != statementQuery {
		return &Rejected{Reason: "the statement is not a query"}
	}
	var result C.lbug_query_result
	state := C.lbug_connection_execute(&d.conn, &prepared, &result)
	defer C.lbug_query_result_destroy(&result)
	if state != C.LbugSuccess || !C.lbug_query_result_is_success(&result) {
		return &Rejected{Reason: takeString(C.lbug_query_result_get_error_message(&result))}
	}
	return read(&result)
}

func rows(r *C.lbug_query_result) (*Result, error) {
	n := uint64(C.lbug_query_result_get_num_columns(r))
	result := &Result{Columns: make([]string, n), Rows: [][]any{}}
	for i := range n {
		var name *C.char
		if C.lbug_query_result_get_column_name(r, C.uint64_t(i), &name) != C.LbugSuccess {
			return nil, fmt.Errorf("read the name of column %d", i)
		}
		result.Columns[i] = takeString(name)
	}
	for C.lbug_query_result_has_next(r) {
		var tuple C.lbug_flat_tuple
		if C.lbug_query_result_get_next(r, &tuple) != C.LbugSuccess {
			return nil, &Rejected{Reason: lastError()}
		}
		row := make([]any, n)
		for i := range n {
			var v C.lbug_value
			if C.lbug_flat_tuple_get_value(&tuple, C.uint64_t(i), &v) != C.LbugSuccess {
				C.lbug_flat_tuple_destroy(&tuple)
				return nil, fmt.Errorf("read column %d of a row", i)
			}
			value, err := goValue(&v)
			if err != nil {
				C.lbug_flat_tuple_destroy(&tuple)
				return nil, err
			}
			row[i] = value
		}
		C.lbug_flat_tuple_destroy(&tuple)
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}

var errValue = errors.New("read a value of the result")

// goValue converts a value that the engine owns; nested values are owned by their parent and are not destroyed.
func goValue(v *C.lbug_value) (any, error) {
	if C.lbug_value_is_null(v) {
		return nil, nil
	}
	var logical C.lbug_logical_type
	C.lbug_value_get_data_type(v, &logical)
	defer C.lbug_data_type_destroy(&logical)
	switch C.lbug_data_type_get_id(&logical) {
	case C.LBUG_BOOL:
		var b C.bool
		err := check(C.lbug_value_get_bool(v, &b))
		return bool(b), err
	case C.LBUG_INT64, C.LBUG_SERIAL:
		var i C.int64_t
		err := check(C.lbug_value_get_int64(v, &i))
		return int64(i), err
	case C.LBUG_INT32:
		var i C.int32_t
		err := check(C.lbug_value_get_int32(v, &i))
		return int64(i), err
	case C.LBUG_INT16:
		var i C.int16_t
		err := check(C.lbug_value_get_int16(v, &i))
		return int64(i), err
	case C.LBUG_INT8:
		var i C.int8_t
		err := check(C.lbug_value_get_int8(v, &i))
		return int64(i), err
	case C.LBUG_UINT64:
		var i C.uint64_t
		err := check(C.lbug_value_get_uint64(v, &i))
		return uint64(i), err
	case C.LBUG_UINT32:
		var i C.uint32_t
		err := check(C.lbug_value_get_uint32(v, &i))
		return int64(i), err
	case C.LBUG_UINT16:
		var i C.uint16_t
		err := check(C.lbug_value_get_uint16(v, &i))
		return int64(i), err
	case C.LBUG_UINT8:
		var i C.uint8_t
		err := check(C.lbug_value_get_uint8(v, &i))
		return int64(i), err
	case C.LBUG_DOUBLE:
		var f C.double
		err := check(C.lbug_value_get_double(v, &f))
		return float64(f), err
	case C.LBUG_FLOAT:
		var f C.float
		err := check(C.lbug_value_get_float(v, &f))
		return float64(f), err
	case C.LBUG_STRING:
		var s *C.char
		if err := check(C.lbug_value_get_string(v, &s)); err != nil {
			return nil, err
		}
		return takeString(s), nil
	case C.LBUG_LIST:
		var n C.uint64_t
		if err := check(C.lbug_value_get_list_size(v, &n)); err != nil {
			return nil, err
		}
		return elements(v, uint64(n))
	case C.LBUG_ARRAY:
		var n C.uint64_t
		if err := check(C.lbug_data_type_get_num_elements_in_array(&logical, &n)); err != nil {
			return nil, err
		}
		return elements(v, uint64(n))
	case C.LBUG_MAP:
		var n C.uint64_t
		if err := check(C.lbug_value_get_map_size(v, &n)); err != nil {
			return nil, err
		}
		return elements(v, uint64(n))
	case C.LBUG_NODE, C.LBUG_REL, C.LBUG_RECURSIVE_REL, C.LBUG_STRUCT, C.LBUG_UNION:
		return fields(v)
	default:
		return takeString(C.lbug_value_to_string(v)), nil
	}
}

func elements(v *C.lbug_value, n uint64) ([]any, error) {
	list := make([]any, n)
	for i := range n {
		var element C.lbug_value
		if err := check(C.lbug_value_get_list_element(v, C.uint64_t(i), &element)); err != nil {
			return nil, err
		}
		value, err := goValue(&element)
		if err != nil {
			return nil, err
		}
		list[i] = value
	}
	return list, nil
}

func fields(v *C.lbug_value) ([]Field, error) {
	var n C.uint64_t
	if err := check(C.lbug_value_get_struct_num_fields(v, &n)); err != nil {
		return nil, err
	}
	fs := make([]Field, n)
	for i := range uint64(n) {
		var name *C.char
		if err := check(C.lbug_value_get_struct_field_name(v, C.uint64_t(i), &name)); err != nil {
			return nil, err
		}
		fs[i].Name = takeString(name)
		var field C.lbug_value
		if err := check(C.lbug_value_get_struct_field_value(v, C.uint64_t(i), &field)); err != nil {
			return nil, err
		}
		value, err := goValue(&field)
		if err != nil {
			return nil, err
		}
		fs[i].Value = value
	}
	return fs, nil
}

func check(state C.lbug_state) error {
	if state != C.LbugSuccess {
		return errValue
	}
	return nil
}

func lastError() string {
	if s := takeString(C.lbug_get_last_error()); s != "" {
		return s
	}
	return "the engine gave no reason"
}

func takeString(s *C.char) string {
	if s == nil {
		return ""
	}
	defer C.lbug_destroy_string(s)
	return C.GoString(s)
}
