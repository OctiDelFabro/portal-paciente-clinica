package mysqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

type poolDriver struct{}
type poolConn struct{ inTx bool }
type poolTx struct{ c *poolConn }
type keyRow struct {
	key  string
	done bool
}

func (poolDriver) Open(string) (driver.Conn, error)                              { return &poolConn{}, nil }
func (c *poolConn) Prepare(string) (driver.Stmt, error)                          { return nil, errors.New("unexpected prepare") }
func (c *poolConn) Close() error                                                 { return nil }
func (c *poolConn) Begin() (driver.Tx, error)                                    { c.inTx = true; return &poolTx{c}, nil }
func (c *poolConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c.Begin() }
func (t *poolTx) Commit() error                                                  { t.c.inTx = false; return nil }
func (t *poolTx) Rollback() error                                                { t.c.inTx = false; return nil }
func (c *poolConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if !c.inTx {
		return nil, errors.New("borrowed an extra connection")
	}
	return driver.RowsAffected(1), nil
}
func (c *poolConn) QueryContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	return &keyRow{key: args[0].Value.(string)}, nil
}
func (r *keyRow) Columns() []string { return []string{"resource_key"} }
func (r *keyRow) Close() error      { return nil }
func (r *keyRow) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	values[0] = r.key
	return nil
}
func TestLockResourcesUsesOnlyItsTransactionConnection(t *testing.T) {
	sql.Register("pool-lock-review", poolDriver{})
	db, err := sql.Open("pool-lock-review", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := (&mysqlTx{s: &Store{DB: db}, tx: tx}).LockResources(ctx, "patient:pat-001", "doctor:doc-001"); err != nil {
		t.Fatalf("saturated pool blocked a transaction: %v", err)
	}
	if db.Stats().WaitCount != 0 {
		t.Fatal("borrowed another pooled connection")
	}
}
