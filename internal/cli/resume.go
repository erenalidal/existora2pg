package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/erenalidal/existora2pg/internal/jobstore"
	"github.com/erenalidal/existora2pg/internal/migration"
	"github.com/erenalidal/existora2pg/internal/oracle"
	"github.com/erenalidal/existora2pg/internal/postgres"
	"github.com/spf13/cobra"

	_ "modernc.org/sqlite"
)

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume a failed migration from last checkpoint",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			oraDB, err := oracle.OpenPool(ctx, cfg.Oracle.DSN, cfg.Oracle.MaxConns)
			if err != nil {
				return fmt.Errorf("connect oracle: %w", err)
			}
			defer oraDB.Close()

			pgPool, err := postgres.OpenPool(ctx, cfg.Postgres.DSN, cfg.Postgres.MaxConns)
			if err != nil {
				return fmt.Errorf("connect postgres: %w", err)
			}
			defer pgPool.Close()

			store, err := jobstore.New(cfg.Migration.StateDBPath)
			if err != nil {
				return fmt.Errorf("open state db: %w", err)
			}
			defer store.Close()

			if err := store.InitSchema(ctx); err != nil {
				return fmt.Errorf("init state schema: %w", err)
			}

			extractor := oracle.NewExtractor(oraDB, logger)
			reader := oracle.NewReader(oraDB, cfg.Migration.FetchSize, logger)
			writer := postgres.NewWriter(pgPool, cfg.Postgres.Schema, logger)

			engine := migration.NewEngine(extractor, reader, writer, store, cfg, logger)
			return engine.Resume(ctx)
		},
	}
}
