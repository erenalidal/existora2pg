package postgres

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OpenPool creates a PostgreSQL connection pool using pgx.
func OpenPool(ctx context.Context, dsn string, maxConns int) (*pgxpool.Pool, error) {
	// Ensure connect_timeout is set so TCP doesn't hang
	if !strings.Contains(dsn, "connect_timeout") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "connect_timeout=20"
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg config: %w", err)
	}

	cfg.MaxConns = int32(maxConns)
	cfg.MinConns = int32(max(1, min(4, maxConns/4))) // keep few warm, don't overwhelm remote PG
	cfg.ConnConfig.ConnectTimeout = 30 * time.Second
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	// TCP keepalive prevents firewalls/NAT from silently dropping idle connections.
	// Active during COPY too — OS-level keepalive probes detect dead peers faster.
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		return d.DialContext(ctx, network, addr)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("connection timeout — check host, port, and firewall settings")
		}
		return nil, fmt.Errorf("create pg pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("connection timeout — check host, port, and firewall settings")
		}
		return nil, fmt.Errorf("ping pg: %w", err)
	}

	return pool, nil
}
