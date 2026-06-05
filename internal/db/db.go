// Package db owns the PostgreSQL connection for the application.
//
// It opens a *bun.DB using pgdriver/pgdialect. Atlas owns the schema; Bun is
// used for querying only. Nothing here creates or alters tables.
package db

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// Open creates a *bun.DB connected to the Postgres instance described by dsn.
// It does not verify reachability; call Ping for that.
func Open(dsn string) *bun.DB {
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	return bun.NewDB(sqldb, pgdialect.New())
}

// Ping reports whether the database is reachable.
func Ping(ctx context.Context, db *bun.DB) error {
	return db.PingContext(ctx)
}
