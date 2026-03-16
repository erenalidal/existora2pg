package oracle

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/godror/godror"
)

// OpenPool creates an Oracle connection pool using godror.
func OpenPool(ctx context.Context, dsn string, maxConns int) (*sql.DB, error) {
	db, err := sql.Open("godror", dsn)
	if err != nil {
		return nil, fmt.Errorf("open oracle: %w", err)
	}

	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping oracle: %w", err)
	}

	return db, nil
}
