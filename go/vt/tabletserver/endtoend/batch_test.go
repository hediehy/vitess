// Copyright 2015, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package endtoend

import (
	"reflect"
	"testing"

	"github.com/youtube/vitess/go/sqltypes"
	querypb "github.com/youtube/vitess/go/vt/proto/query"
	"github.com/youtube/vitess/go/vt/tabletserver/endtoend/framework"
	"github.com/youtube/vitess/go/vt/tabletserver/querytypes"
)

func TestBatchRead(t *testing.T) {
	client := framework.NewClient()
	queries := []querytypes.BoundQuery{{
		Sql:           "select * from vitess_a where id = :a",
		BindVariables: map[string]interface{}{"a": 2},
	}, {
		Sql:           "select * from vitess_b where id = :b",
		BindVariables: map[string]interface{}{"b": 2},
	}}
	qr1 := sqltypes.Result{
		Fields: []*querypb.Field{{
			Name: "eid",
			Type: sqltypes.Int64,
		}, {
			Name: "id",
			Type: sqltypes.Int32,
		}, {
			Name: "name",
			Type: sqltypes.VarChar,
		}, {
			Name: "foo",
			Type: sqltypes.VarBinary,
		}},
		RowsAffected: 1,
		Rows: [][]sqltypes.Value{
			[]sqltypes.Value{
				sqltypes.MakeTrusted(sqltypes.Int64, []byte("1")),
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("2")),
				sqltypes.MakeTrusted(sqltypes.VarChar, []byte("bcde")),
				sqltypes.MakeTrusted(sqltypes.VarBinary, []byte("fghi")),
			},
		},
	}
	qr2 := sqltypes.Result{
		Fields: []*querypb.Field{{
			Name: "eid",
			Type: sqltypes.Int64,
		}, {
			Name: "id",
			Type: sqltypes.Int32,
		}},
		RowsAffected: 1,
		Rows: [][]sqltypes.Value{
			[]sqltypes.Value{
				sqltypes.MakeTrusted(sqltypes.Int64, []byte("1")),
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("2")),
			},
		},
	}
	want := []sqltypes.Result{qr1, qr2}

	qrl, err := client.ExecuteBatch(queries, false)
	if err != nil {
		t.Error(err)
		return
	}
	if !reflect.DeepEqual(qrl, want) {
		t.Errorf("ExecueBatch: \n%#v, want \n%#v", qrl, want)
	}
}

func TestBatchTransaction(t *testing.T) {
	client := framework.NewClient()
	queries := []querytypes.BoundQuery{{
		Sql: "insert into vitess_test values(4, null, null, null)",
	}, {
		Sql: "select * from vitess_test where intval = 4",
	}, {
		Sql: "delete from vitess_test where intval = 4",
	}}

	wantRows := [][]sqltypes.Value{
		[]sqltypes.Value{
			sqltypes.MakeTrusted(sqltypes.Int32, []byte("4")),
			sqltypes.Value{},
			sqltypes.Value{},
			sqltypes.Value{},
		},
	}

	// Not in transaction, AsTransaction false
	qrl, err := client.ExecuteBatch(queries, false)
	if err != nil {
		t.Error(err)
		return
	}
	if !reflect.DeepEqual(qrl[1].Rows, wantRows) {
		t.Errorf("Rows: \n%#v, want \n%#v", qrl[1].Rows, wantRows)
	}

	// Not in transaction, AsTransaction true
	qrl, err = client.ExecuteBatch(queries, true)
	if err != nil {
		t.Error(err)
		return
	}
	if !reflect.DeepEqual(qrl[1].Rows, wantRows) {
		t.Errorf("Rows: \n%#v, want \n%#v", qrl[1].Rows, wantRows)
	}

	// In transaction, AsTransaction false
	func() {
		err = client.Begin()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Commit()
		qrl, err = client.ExecuteBatch(queries, false)
		if err != nil {
			t.Error(err)
			return
		}
		if !reflect.DeepEqual(qrl[1].Rows, wantRows) {
			t.Errorf("Rows: \n%#v, want \n%#v", qrl[1].Rows, wantRows)
		}
	}()

	// In transaction, AsTransaction true
	func() {
		err = client.Begin()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Rollback()
		qrl, err = client.ExecuteBatch(queries, true)
		want := "error: cannot start a new transaction in the scope of an existing one"
		if err == nil || err.Error() != want {
			t.Errorf("Error: %v, want %s", err, want)
		}
	}()
}
