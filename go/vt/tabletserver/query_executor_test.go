// Copyright 2015, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tabletserver

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/youtube/vitess/go/mysql"
	"github.com/youtube/vitess/go/sqldb"
	"github.com/youtube/vitess/go/sqltypes"
	"github.com/youtube/vitess/go/vt/callerid"
	"github.com/youtube/vitess/go/vt/callinfo"
	topodatapb "github.com/youtube/vitess/go/vt/proto/topodata"
	"github.com/youtube/vitess/go/vt/tableacl"
	"github.com/youtube/vitess/go/vt/tableacl/simpleacl"
	"github.com/youtube/vitess/go/vt/tabletserver/fakecacheservice"
	"github.com/youtube/vitess/go/vt/tabletserver/planbuilder"
	"github.com/youtube/vitess/go/vt/vttest/fakesqldb"
	"golang.org/x/net/context"

	querypb "github.com/youtube/vitess/go/vt/proto/query"
	tableaclpb "github.com/youtube/vitess/go/vt/proto/tableacl"
)

func TestQueryExecutorPlanDDL(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "alter table test_table add zipcode int"
	want := &sqltypes.Result{
		Rows: [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanDDL, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanPassDmlStrictMode(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "update test_table set pk = foo()"
	want := &sqltypes.Result{
		Rows: [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	ctx := context.Background()
	// non strict mode
	tsv := newTestTabletServer(ctx, noFlags, db)
	qre := newTestQueryExecutor(ctx, tsv, query, newTransaction(tsv))
	checkPlanID(t, planbuilder.PlanPassDML, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
	testCommitHelper(t, tsv, qre)
	tsv.StopService()

	// strict mode
	tsv = newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre = newTestQueryExecutor(ctx, tsv, query, newTransaction(tsv))
	defer tsv.StopService()
	defer testCommitHelper(t, tsv, qre)
	checkPlanID(t, planbuilder.PlanPassDML, qre.plan.PlanID)
	got, err = qre.Execute()
	if err == nil {
		t.Fatal("qre.Execute() = nil, want error")
	}
	tabletError, ok := err.(*TabletError)
	if !ok {
		t.Fatalf("got: %v, want: a TabletError", tabletError)
	}
	if tabletError.ErrorType != ErrFail {
		t.Fatalf("got: %s, want: ErrFail", getTabletErrorString(ErrFail))
	}
}

func TestQueryExecutorPlanPassDmlStrictModeAutoCommit(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "update test_table set pk = foo()"
	want := &sqltypes.Result{
		Rows: [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	// non strict mode
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, noFlags, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	checkPlanID(t, planbuilder.PlanPassDML, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
	tsv.StopService()

	// strict mode
	// update should fail because strict mode is not enabled
	tsv = newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre = newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanPassDML, qre.plan.PlanID)
	_, err = qre.Execute()
	if err == nil {
		t.Fatal("got: nil, want: error")
	}
	tabletError, ok := err.(*TabletError)
	if !ok {
		t.Fatalf("got: %v, want: *TabletError", tabletError)
	}
	if tabletError.ErrorType != ErrFail {
		t.Fatalf("got: %s, want: ErrFail", getTabletErrorString(ErrFail))
	}
}

func TestQueryExecutorPlanInsertPk(t *testing.T) {
	db := setUpQueryExecutorTest()
	db.AddQuery("insert into test_table values (1) /* _stream test_table (pk ) (1 ); */", &sqltypes.Result{})
	want := &sqltypes.Result{
		Rows: make([][]sqltypes.Value, 0),
	}
	query := "insert into test_table values(1)"
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanInsertPK, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanInsertSubQueryAutoCommmit(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "insert into test_table(pk) select pk from test_table where pk = 1 limit 1000"
	want := &sqltypes.Result{
		Rows: [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	selectQuery := "select pk from test_table where pk = 1 limit 1000"
	db.AddQuery(selectQuery, &sqltypes.Result{
		RowsAffected: 1,
		Rows: [][]sqltypes.Value{
			[]sqltypes.Value{sqltypes.MakeTrusted(sqltypes.Int32, []byte("2"))},
		},
	})

	insertQuery := "insert into test_table(pk) values (2) /* _stream test_table (pk ) (2 ); */"

	db.AddQuery(insertQuery, &sqltypes.Result{})
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanInsertSubquery, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanInsertSubQuery(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "insert into test_table(pk) select pk from test_table where pk = 1 limit 1000"
	want := &sqltypes.Result{
		Rows: [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	selectQuery := "select pk from test_table where pk = 1 limit 1000"
	db.AddQuery(selectQuery, &sqltypes.Result{
		RowsAffected: 1,
		Rows: [][]sqltypes.Value{
			[]sqltypes.Value{sqltypes.MakeTrusted(sqltypes.Int32, []byte("2"))},
		},
	})

	insertQuery := "insert into test_table(pk) values (2) /* _stream test_table (pk ) (2 ); */"

	db.AddQuery(insertQuery, &sqltypes.Result{})
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, newTransaction(tsv))

	defer tsv.StopService()
	defer testCommitHelper(t, tsv, qre)
	checkPlanID(t, planbuilder.PlanInsertSubquery, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanUpsertPk(t *testing.T) {
	db := setUpQueryExecutorTest()
	db.AddQuery("insert into test_table values (1) /* _stream test_table (pk ) (1 ); */", &sqltypes.Result{})
	want := &sqltypes.Result{
		Rows: make([][]sqltypes.Value, 0),
	}
	query := "insert into test_table values(1) on duplicate key update val=1"
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanUpsertPK, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}

	db.AddRejectedQuery("insert into test_table values (1) /* _stream test_table (pk ) (1 ); */", errRejected)
	_, err = qre.Execute()
	wantErr := "error: rejected"
	if err == nil || err.Error() != wantErr {
		t.Fatalf("qre.Execute() = %v, want %v", err, wantErr)
	}

	db.AddRejectedQuery(
		"insert into test_table values (1) /* _stream test_table (pk ) (1 ); */",
		sqldb.NewSQLError(mysql.ErrDupEntry, "err"),
	)
	db.AddQuery("update test_table set val = 1 where pk in (1) /* _stream test_table (pk ) (1 ); */", &sqltypes.Result{})
	_, err = qre.Execute()
	wantErr = "error: err (errno 1062)"
	if err == nil || err.Error() != wantErr {
		t.Fatalf("qre.Execute() = %v, want %v", err, wantErr)
	}

	db.AddRejectedQuery(
		"insert into test_table values (1) /* _stream test_table (pk ) (1 ); */",
		sqldb.NewSQLError(mysql.ErrDupEntry, "ERROR 1062 (23000): Duplicate entry '2' for key 'PRIMARY'"),
	)
	db.AddQuery(
		"update test_table set val = 1 where pk in (1) /* _stream test_table (pk ) (1 ); */",
		&sqltypes.Result{RowsAffected: 1},
	)
	got, err = qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	want = &sqltypes.Result{
		RowsAffected: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanDmlPk(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "update test_table set name = 2 where pk in (1) /* _stream test_table (pk ) (1 ); */"
	want := &sqltypes.Result{}
	db.AddQuery(query, want)
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, newTransaction(tsv))
	defer tsv.StopService()
	defer testCommitHelper(t, tsv, qre)
	checkPlanID(t, planbuilder.PlanDMLPK, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanDmlAutoCommit(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "update test_table set name = 2 where pk in (1) /* _stream test_table (pk ) (1 ); */"
	want := &sqltypes.Result{}
	db.AddQuery(query, want)
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanDMLPK, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanDmlSubQuery(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "update test_table set addr = 3 where name = 1 limit 1000"
	expandedQuery := "select pk from test_table where name = 1 limit 1000 for update"
	want := &sqltypes.Result{}
	db.AddQuery(query, want)
	db.AddQuery(expandedQuery, want)
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, newTransaction(tsv))
	defer tsv.StopService()
	defer testCommitHelper(t, tsv, qre)
	checkPlanID(t, planbuilder.PlanDMLSubquery, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanDmlSubQueryAutoCommit(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "update test_table set addr = 3 where name = 1 limit 1000"
	expandedQuery := "select pk from test_table where name = 1 limit 1000 for update"
	want := &sqltypes.Result{}
	db.AddQuery(query, want)
	db.AddQuery(expandedQuery, want)
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanDMLSubquery, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanOtherWithinATransaction(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "show test_table"
	want := &sqltypes.Result{
		Fields:       getTestTableFields(),
		RowsAffected: 0,
		Rows:         [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, newTransaction(tsv))
	defer tsv.StopService()
	defer testCommitHelper(t, tsv, qre)
	checkPlanID(t, planbuilder.PlanOther, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanPassSelectWithInATransaction(t *testing.T) {
	db := setUpQueryExecutorTest()
	fields := []*querypb.Field{
		&querypb.Field{Name: "addr", Type: sqltypes.Int32},
	}
	query := "select addr from test_table where pk = 1 limit 1000"
	want := &sqltypes.Result{
		Fields:       fields,
		RowsAffected: 1,
		Rows: [][]sqltypes.Value{
			[]sqltypes.Value{sqltypes.MakeString([]byte("123"))},
		},
	}
	db.AddQuery(query, want)
	db.AddQuery("select addr from test_table where 1 != 1", &sqltypes.Result{
		Fields: fields,
	})
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, newTransaction(tsv))
	defer tsv.StopService()
	defer testCommitHelper(t, tsv, qre)
	checkPlanID(t, planbuilder.PlanPassSelect, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanPassSelectWithLockOutsideATransaction(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "select * from test_table for update"
	want := &sqltypes.Result{
		Fields: getTestTableFields(),
		Rows:   [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanPassSelect, qre.plan.PlanID)
	_, err := qre.Execute()
	if err == nil {
		t.Fatal("got: nil, want: error")
	}
	got, ok := err.(*TabletError)
	if !ok {
		t.Fatalf("got: %v, want: *TabletError", err)
	}
	if got.ErrorType != ErrFail {
		t.Fatalf("got: %s, want: ErrFail", getTabletErrorString(got.ErrorType))
	}
}

func TestQueryExecutorPlanPassSelect(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "select * from test_table limit 1000"
	want := &sqltypes.Result{
		Fields: getTestTableFields(),
		Rows:   [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanPassSelect, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanPKIn(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "select * from test_table where pk in (1, 2, 3) limit 1000"
	expandedQuery := "select pk, name, addr from test_table where pk in (1, 2, 3)"
	want := &sqltypes.Result{
		Fields:       getTestTableFields(),
		RowsAffected: 1,
		Rows: [][]sqltypes.Value{
			[]sqltypes.Value{
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("1")),
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("20")),
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("30")),
			},
		},
	}
	db.AddQuery(query, want)
	db.AddQuery(expandedQuery, want)
	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanPKIn, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}

	cachedQuery := "select pk, name, addr from test_table where pk in (1)"
	db.AddQuery(cachedQuery, &sqltypes.Result{
		Fields:       getTestTableFields(),
		RowsAffected: 1,
		Rows: [][]sqltypes.Value{
			[]sqltypes.Value{
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("1")),
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("20")),
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("30")),
			},
		},
	})

	nonCachedQuery := "select pk, name, addr from test_table where pk in (2, 3)"
	db.AddQuery(nonCachedQuery, &sqltypes.Result{})
	db.AddQuery(cachedQuery, want)
	// run again, this time pk=1 should hit the rowcache
	got, err = qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanSelectSubQuery(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "select * from test_table where name = 1 limit 1000"
	expandedQuery := "select pk from test_table use index (`index`) where name = 1 limit 1000"
	want := &sqltypes.Result{
		Fields: getTestTableFields(),
	}
	db.AddQuery(query, want)
	db.AddQuery(expandedQuery, want)

	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanSelectSubquery, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanSet(t *testing.T) {
	db := setUpQueryExecutorTest()
	setQuery := "set unknown_key = 1"
	db.AddQuery(setQuery, &sqltypes.Result{})
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	defer tsv.StopService()
	qre := newTestQueryExecutor(ctx, tsv, setQuery, 0)
	checkPlanID(t, planbuilder.PlanSet, qre.plan.PlanID)
	// Query will be delegated to MySQL and both Fields and Rows should be
	// empty arrays in this case.
	want := &sqltypes.Result{
		Rows: make([][]sqltypes.Value, 0),
	}
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qre.Execute() = %v, want: %v", got, want)
	}
}

func TestQueryExecutorPlanOther(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "show test_table"
	want := &sqltypes.Result{
		Fields:       getTestTableFields(),
		RowsAffected: 0,
		Rows:         [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	ctx := context.Background()
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanOther, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("got: %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qre.Execute() = %v, want: %v", got, want)
	}
}

func TestQueryExecutorTableAcl(t *testing.T) {
	aclName := fmt.Sprintf("simpleacl-test-%d", rand.Int63())
	tableacl.Register(aclName, &simpleacl.Factory{})
	tableacl.SetDefaultACL(aclName)
	db := setUpQueryExecutorTest()
	query := "select * from test_table limit 1000"
	want := &sqltypes.Result{
		Fields:       getTestTableFields(),
		RowsAffected: 0,
		Rows:         [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})

	username := "u2"
	callerID := &querypb.VTGateCallerID{
		Username: username,
	}
	ctx := callerid.NewContext(context.Background(), nil, callerID)
	config := &tableaclpb.Config{
		TableGroups: []*tableaclpb.TableGroupSpec{{
			Name:                 "group01",
			TableNamesOrPrefixes: []string{"test_table"},
			Readers:              []string{username},
		}},
	}
	if err := tableacl.InitFromProto(config); err != nil {
		t.Fatalf("unable to load tableacl config, error: %v", err)
	}

	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanPassSelect, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("got: %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qre.Execute() = %v, want: %v", got, want)
	}
}

func TestQueryExecutorTableAclNoPermission(t *testing.T) {
	aclName := fmt.Sprintf("simpleacl-test-%d", rand.Int63())
	tableacl.Register(aclName, &simpleacl.Factory{})
	tableacl.SetDefaultACL(aclName)
	db := setUpQueryExecutorTest()
	query := "select * from test_table limit 1000"
	want := &sqltypes.Result{
		Fields:       getTestTableFields(),
		RowsAffected: 0,
		Rows:         [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})

	username := "u2"
	callerID := &querypb.VTGateCallerID{
		Username: username,
	}
	ctx := callerid.NewContext(context.Background(), nil, callerID)
	config := &tableaclpb.Config{
		TableGroups: []*tableaclpb.TableGroupSpec{{
			Name:                 "group02",
			TableNamesOrPrefixes: []string{"test_table"},
			Readers:              []string{"superuser"},
		}},
	}

	if err := tableacl.InitFromProto(config); err != nil {
		t.Fatalf("unable to load tableacl config, error: %v", err)
	}
	// without enabling Config.StrictTableAcl
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	checkPlanID(t, planbuilder.PlanPassSelect, qre.plan.PlanID)
	got, err := qre.Execute()
	if err != nil {
		t.Fatalf("got: %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qre.Execute() = %v, want: %v", got, want)
	}
	tsv.StopService()

	// enable Config.StrictTableAcl
	tsv = newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict|enableStrictTableAcl, db)
	qre = newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanPassSelect, qre.plan.PlanID)
	// query should fail because current user do not have read permissions
	_, err = qre.Execute()
	if err == nil {
		t.Fatal("got: nil, want: error")
	}
	tabletError, ok := err.(*TabletError)
	if !ok {
		t.Fatalf("got: %v, want: *TabletError", err)
	}
	if tabletError.ErrorType != ErrFail {
		t.Fatalf("got: %s, want: ErrFail", getTabletErrorString(tabletError.ErrorType))
	}
}

func TestQueryExecutorTableAclExemptACL(t *testing.T) {
	aclName := fmt.Sprintf("simpleacl-test-%d", rand.Int63())
	tableacl.Register(aclName, &simpleacl.Factory{})
	tableacl.SetDefaultACL(aclName)
	db := setUpQueryExecutorTest()
	query := "select * from test_table limit 1000"
	want := &sqltypes.Result{
		Fields:       getTestTableFields(),
		RowsAffected: 0,
		Rows:         [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})

	username := "u2"
	callerID := &querypb.VTGateCallerID{
		Username: username,
	}
	ctx := callerid.NewContext(context.Background(), nil, callerID)

	config := &tableaclpb.Config{
		TableGroups: []*tableaclpb.TableGroupSpec{{
			Name:                 "group02",
			TableNamesOrPrefixes: []string{"test_table"},
			Readers:              []string{"u1"},
		}},
	}

	if err := tableacl.InitFromProto(config); err != nil {
		t.Fatalf("unable to load tableacl config, error: %v", err)
	}

	// enable Config.StrictTableAcl
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict|enableStrictTableAcl, db)
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanPassSelect, qre.plan.PlanID)
	// query should fail because current user do not have read permissions
	_, err := qre.Execute()
	if err == nil {
		t.Fatal("got: nil, want: error")
	}
	tabletError, ok := err.(*TabletError)
	if !ok {
		t.Fatalf("got: %v, want: *TabletError", err)
	}
	if tabletError.ErrorType != ErrFail {
		t.Fatalf("got: %s, want: ErrFail", getTabletErrorString(tabletError.ErrorType))
	}
	if !strings.Contains(tabletError.Error(), "table acl error") {
		t.Fatalf("got %s, want tablet errorL table acl error", tabletError.Error())
	}

	// table acl should be ignored since this is an exempt user.
	username = "exempt-acl"
	f, _ := tableacl.GetCurrentAclFactory()
	if tsv.qe.exemptACL, err = f.New([]string{username}); err != nil {
		t.Fatalf("Cannot load exempt ACL for Table ACL: %v", err)
	}
	callerID = &querypb.VTGateCallerID{
		Username: username,
	}
	ctx = callerid.NewContext(context.Background(), nil, callerID)

	qre = newTestQueryExecutor(ctx, tsv, query, 0)
	_, err = qre.Execute()
	if err != nil {
		t.Fatal("qre.Execute: nil, want: error")
	}
}

func TestQueryExecutorTableAclDryRun(t *testing.T) {
	aclName := fmt.Sprintf("simpleacl-test-%d", rand.Int63())
	tableacl.Register(aclName, &simpleacl.Factory{})
	tableacl.SetDefaultACL(aclName)
	db := setUpQueryExecutorTest()
	query := "select * from test_table limit 1000"
	want := &sqltypes.Result{
		Fields:       getTestTableFields(),
		RowsAffected: 0,
		Rows:         [][]sqltypes.Value{},
	}
	db.AddQuery(query, want)
	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})

	username := "u2"
	callerID := &querypb.VTGateCallerID{
		Username: username,
	}
	ctx := callerid.NewContext(context.Background(), nil, callerID)

	config := &tableaclpb.Config{
		TableGroups: []*tableaclpb.TableGroupSpec{{
			Name:                 "group02",
			TableNamesOrPrefixes: []string{"test_table"},
			Readers:              []string{"u1"},
		}},
	}

	if err := tableacl.InitFromProto(config); err != nil {
		t.Fatalf("unable to load tableacl config, error: %v", err)
	}

	tableACLStatsKey := strings.Join([]string{
		"test_table",
		"group02",
		planbuilder.PlanPassSelect.String(),
		username,
	}, ".")
	// enable Config.StrictTableAcl
	tsv := newTestTabletServer(ctx, enableRowCache|enableSchemaOverrides|enableStrict|enableStrictTableAcl, db)
	tsv.qe.enableTableAclDryRun = true
	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()
	checkPlanID(t, planbuilder.PlanPassSelect, qre.plan.PlanID)
	beforeCount := tsv.qe.tableaclPseudoDenied.Counters.Counts()[tableACLStatsKey]
	// query should fail because current user do not have read permissions
	_, err := qre.Execute()
	if err != nil {
		t.Fatalf("qre.Execute() = %v, want: nil", err)
	}
	afterCount := tsv.qe.tableaclPseudoDenied.Counters.Counts()[tableACLStatsKey]
	if afterCount-beforeCount != 1 {
		t.Fatalf("table acl pseudo denied count should increase by one. got: %d, want: %d", afterCount, beforeCount+1)
	}
}

func TestQueryExecutorBlacklistQRFail(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "select * from test_table where name = 1 limit 1000"
	expandedQuery := "select pk from test_table use index (`index`) where name = 1 limit 1000"
	expected := &sqltypes.Result{
		Fields: getTestTableFields(),
	}
	db.AddQuery(query, expected)
	db.AddQuery(expandedQuery, expected)

	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})

	bannedAddr := "127.0.0.1"
	bannedUser := "u2"

	alterRule := NewQueryRule("disable update", "disable update", QRFail)
	alterRule.SetIPCond(bannedAddr)
	alterRule.SetUserCond(bannedUser)
	alterRule.SetQueryCond("select.*")
	alterRule.AddPlanCond(planbuilder.PlanSelectSubquery)
	alterRule.AddTableCond("test_table")

	rulesName := "blacklistedRulesQRFail"
	rules := NewQueryRules()
	rules.Add(alterRule)

	callInfo := &fakeCallInfo{
		remoteAddr: bannedAddr,
		username:   bannedUser,
	}
	ctx := callinfo.NewContext(context.Background(), callInfo)
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	tsv.qe.schemaInfo.queryRuleSources.UnRegisterQueryRuleSource(rulesName)
	tsv.qe.schemaInfo.queryRuleSources.RegisterQueryRuleSource(rulesName)
	defer tsv.qe.schemaInfo.queryRuleSources.UnRegisterQueryRuleSource(rulesName)

	if err := tsv.qe.schemaInfo.queryRuleSources.SetRules(rulesName, rules); err != nil {
		t.Fatalf("failed to set rule, error: %v", err)
	}

	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()

	checkPlanID(t, planbuilder.PlanSelectSubquery, qre.plan.PlanID)
	// execute should fail because query has been blacklisted
	_, err := qre.Execute()
	if err == nil {
		t.Fatal("got: nil, want: error")
	}
	got, ok := err.(*TabletError)
	if !ok {
		t.Fatalf("got: %v, want: *TabletError", err)
	}
	if got.ErrorType != ErrFail {
		t.Fatalf("got: %s, want: ErrFail", getTabletErrorString(got.ErrorType))
	}
}

func TestQueryExecutorBlacklistQRRetry(t *testing.T) {
	db := setUpQueryExecutorTest()
	query := "select * from test_table where name = 1 limit 1000"
	expandedQuery := "select pk from test_table use index (`index`) where name = 1 limit 1000"
	expected := &sqltypes.Result{
		Fields: getTestTableFields(),
	}
	db.AddQuery(query, expected)
	db.AddQuery(expandedQuery, expected)

	db.AddQuery("select * from test_table where 1 != 1", &sqltypes.Result{
		Fields: getTestTableFields(),
	})

	bannedAddr := "127.0.0.1"
	bannedUser := "x"

	alterRule := NewQueryRule("disable update", "disable update", QRFailRetry)
	alterRule.SetIPCond(bannedAddr)
	alterRule.SetUserCond(bannedUser)
	alterRule.SetQueryCond("select.*")
	alterRule.AddPlanCond(planbuilder.PlanSelectSubquery)
	alterRule.AddTableCond("test_table")

	rulesName := "blacklistedRulesQRRetry"
	rules := NewQueryRules()
	rules.Add(alterRule)

	callInfo := &fakeCallInfo{
		remoteAddr: bannedAddr,
		username:   bannedUser,
	}
	ctx := callinfo.NewContext(context.Background(), callInfo)
	tsv := newTestTabletServer(ctx, enableRowCache|enableStrict, db)
	tsv.qe.schemaInfo.queryRuleSources.UnRegisterQueryRuleSource(rulesName)
	tsv.qe.schemaInfo.queryRuleSources.RegisterQueryRuleSource(rulesName)
	defer tsv.qe.schemaInfo.queryRuleSources.UnRegisterQueryRuleSource(rulesName)

	if err := tsv.qe.schemaInfo.queryRuleSources.SetRules(rulesName, rules); err != nil {
		t.Fatalf("failed to set rule, error: %v", err)
	}

	qre := newTestQueryExecutor(ctx, tsv, query, 0)
	defer tsv.StopService()

	checkPlanID(t, planbuilder.PlanSelectSubquery, qre.plan.PlanID)
	_, err := qre.Execute()
	if err == nil {
		t.Fatal("got: nil, want: error")
	}
	got, ok := err.(*TabletError)
	if !ok {
		t.Fatalf("got: %v, want: *TabletError", err)
	}
	if got.ErrorType != ErrRetry {
		t.Fatalf("got: %s, want: ErrRetry", getTabletErrorString(got.ErrorType))
	}
}

type executorFlags int64

const (
	noFlags        executorFlags = iota
	enableRowCache               = 1 << iota
	enableSchemaOverrides
	enableStrict
	enableStrictTableAcl
)

// newTestQueryExecutor uses a package level variable testTabletServer defined in tabletserver_test.go
func newTestTabletServer(ctx context.Context, flags executorFlags, db *fakesqldb.DB) *TabletServer {
	randID := rand.Int63()
	config := DefaultQsConfig
	config.StatsPrefix = fmt.Sprintf("Stats-%d-", randID)
	config.DebugURLPrefix = fmt.Sprintf("/debug-%d-", randID)
	config.RowCache.StatsPrefix = fmt.Sprintf("Stats-%d-", randID)
	config.PoolNamePrefix = fmt.Sprintf("Pool-%d-", randID)
	config.PoolSize = 100
	config.TransactionCap = 100
	config.SpotCheckRatio = 1.0
	config.EnablePublishStats = false
	config.EnableAutoCommit = true

	if flags&enableStrict > 0 {
		config.StrictMode = true
	} else {
		config.StrictMode = false
	}
	if flags&enableRowCache > 0 {
		config.RowCache.Enabled = true
		config.RowCache.Binary = "ls"
		config.RowCache.Connections = 100
	}
	if flags&enableStrictTableAcl > 0 {
		config.StrictTableAcl = true
	} else {
		config.StrictTableAcl = false
	}
	tsv := NewTabletServer(config)
	testUtils := newTestUtils()
	dbconfigs := testUtils.newDBConfigs(db)
	schemaOverrides := []SchemaOverride{}
	if flags&enableSchemaOverrides > 0 {
		schemaOverrides = getTestTableSchemaOverrides()
	}
	target := querypb.Target{TabletType: topodatapb.TabletType_MASTER}
	tsv.StartService(target, dbconfigs, schemaOverrides, testUtils.newMysqld(&dbconfigs))
	return tsv
}

func newTransaction(tsv *TabletServer) int64 {
	transactionID, err := tsv.Begin(context.Background(), &tsv.target, tsv.sessionID)
	if err != nil {
		panic(fmt.Errorf("failed to start a transaction: %v", err))
	}
	return transactionID
}

func newTestQueryExecutor(ctx context.Context, tsv *TabletServer, sql string, txID int64) *QueryExecutor {
	logStats := newLogStats("TestQueryExecutor", ctx)
	return &QueryExecutor{
		ctx:           ctx,
		query:         sql,
		bindVars:      make(map[string]interface{}),
		transactionID: txID,
		plan:          tsv.qe.schemaInfo.GetPlan(ctx, logStats, sql),
		logStats:      logStats,
		qe:            tsv.qe,
	}
}

func testCommitHelper(t *testing.T, tsv *TabletServer, queryExecutor *QueryExecutor) {
	if err := tsv.Commit(queryExecutor.ctx, &tsv.target, tsv.sessionID, queryExecutor.transactionID); err != nil {
		t.Fatalf("failed to commit transaction: %d, err: %v", queryExecutor.transactionID, err)
	}
}

func setUpQueryExecutorTest() *fakesqldb.DB {
	fakecacheservice.Register()
	db := fakesqldb.Register()
	initQueryExecutorTestDB(db)
	return db
}

func initQueryExecutorTestDB(db *fakesqldb.DB) {
	for query, result := range getQueryExecutorSupportedQueries() {
		db.AddQuery(query, result)
	}
}

func getTestTableFields() []*querypb.Field {
	return []*querypb.Field{
		&querypb.Field{Name: "pk", Type: sqltypes.Int32},
		&querypb.Field{Name: "name", Type: sqltypes.Int32},
		&querypb.Field{Name: "addr", Type: sqltypes.Int32},
	}
}

func checkPlanID(
	t *testing.T,
	expectedPlanID planbuilder.PlanType,
	actualPlanID planbuilder.PlanType) {
	if expectedPlanID != actualPlanID {
		t.Fatalf("expect to get PlanID: %s, but got %s",
			expectedPlanID.String(), actualPlanID.String())
	}
}

func getTestTableSchemaOverrides() []SchemaOverride {
	return []SchemaOverride{
		SchemaOverride{
			Name:      "test_table",
			PKColumns: []string{"pk"},
			Cache: &struct {
				Type  string
				Table string
			}{
				Type:  "RW",
				Table: "test_table",
			},
		},
	}
}

func getQueryExecutorSupportedQueries() map[string]*sqltypes.Result {
	return map[string]*sqltypes.Result{
		// queries for schema info
		"select unix_timestamp()": &sqltypes.Result{
			RowsAffected: 1,
			Rows: [][]sqltypes.Value{
				[]sqltypes.Value{sqltypes.MakeTrusted(sqltypes.Int32, []byte("1427325875"))},
			},
		},
		"select @@global.sql_mode": &sqltypes.Result{
			RowsAffected: 1,
			Rows: [][]sqltypes.Value{
				[]sqltypes.Value{sqltypes.MakeString([]byte("STRICT_TRANS_TABLES"))},
			},
		},
		baseShowTables: &sqltypes.Result{
			RowsAffected: 1,
			Rows: [][]sqltypes.Value{
				[]sqltypes.Value{
					sqltypes.MakeString([]byte("test_table")),
					sqltypes.MakeString([]byte("USER TABLE")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("1427325875")),
					sqltypes.MakeString([]byte("")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("1")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("2")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("3")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("4")),
				},
			},
		},
		"select * from `test_table` where 1 != 1": &sqltypes.Result{
			Fields: []*querypb.Field{{
				Name: "pk",
				Type: sqltypes.Int32,
			}, {
				Name: "name",
				Type: sqltypes.Int32,
			}, {
				Name: "addr",
				Type: sqltypes.Int32,
			}},
		},
		"describe `test_table`": &sqltypes.Result{
			RowsAffected: 3,
			Rows: [][]sqltypes.Value{
				[]sqltypes.Value{
					sqltypes.MakeString([]byte("pk")),
					sqltypes.MakeString([]byte("int")),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("1")),
					sqltypes.MakeString([]byte{}),
				},
				[]sqltypes.Value{
					sqltypes.MakeString([]byte("name")),
					sqltypes.MakeString([]byte("int")),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("1")),
					sqltypes.MakeString([]byte{}),
				},
				[]sqltypes.Value{
					sqltypes.MakeString([]byte("addr")),
					sqltypes.MakeString([]byte("int")),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("1")),
					sqltypes.MakeString([]byte{}),
				},
			},
		},
		// for SplitQuery because it needs a primary key column
		"show index from `test_table`": &sqltypes.Result{
			RowsAffected: 2,
			Rows: [][]sqltypes.Value{
				[]sqltypes.Value{
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("PRIMARY")),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("pk")),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("300")),
				},
				[]sqltypes.Value{
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("index")),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("name")),
					sqltypes.MakeString([]byte{}),
					sqltypes.MakeString([]byte("300")),
				},
			},
		},
		"begin":  &sqltypes.Result{},
		"commit": &sqltypes.Result{},
		baseShowTables + " and table_name = 'test_table'": &sqltypes.Result{
			RowsAffected: 1,
			Rows: [][]sqltypes.Value{
				[]sqltypes.Value{
					sqltypes.MakeString([]byte("test_table")),
					sqltypes.MakeString([]byte("USER TABLE")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("1427325875")),
					sqltypes.MakeString([]byte("")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("1")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("2")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("3")),
					sqltypes.MakeTrusted(sqltypes.Int32, []byte("4")),
				},
			},
		},
		"rollback": &sqltypes.Result{},
	}
}
