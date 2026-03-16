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

var dryRun bool

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run Oracle to PostgreSQL migration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			if dryRun {
				return runDryRun(ctx)
			}

			return runMigration(ctx)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "generate DDL without executing")

	return cmd
}

func runMigration(ctx context.Context) error {
	// Connect Oracle
	logger.Info("connecting to Oracle")
	oraDB, err := oracle.OpenPool(ctx, cfg.Oracle.DSN, cfg.Oracle.MaxConns)
	if err != nil {
		return fmt.Errorf("connect oracle: %w", err)
	}
	defer oraDB.Close()

	// Connect PostgreSQL
	logger.Info("connecting to PostgreSQL")
	pgPool, err := postgres.OpenPool(ctx, cfg.Postgres.DSN, cfg.Postgres.MaxConns)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pgPool.Close()

	// Init job store
	store, err := jobstore.New(cfg.Migration.StateDBPath)
	if err != nil {
		return fmt.Errorf("open state db: %w", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		return fmt.Errorf("init state schema: %w", err)
	}

	// Build engine
	extractor := oracle.NewExtractor(oraDB, logger)
	reader := oracle.NewReader(oraDB, cfg.Migration.FetchSize, logger)
	writer := postgres.NewWriter(pgPool, cfg.Postgres.Schema, logger)

	engine := migration.NewEngine(extractor, reader, writer, store, cfg, logger)
	return engine.Run(ctx)
}

func runDryRun(ctx context.Context) error {
	logger.Info("dry-run mode: generating DDL only")

	oraDB, err := oracle.OpenPool(ctx, cfg.Oracle.DSN, cfg.Oracle.MaxConns)
	if err != nil {
		return fmt.Errorf("connect oracle: %w", err)
	}
	defer oraDB.Close()

	extractor := oracle.NewExtractor(oraDB, logger)
	filter := oracle.TableFilter{
		Include:          cfg.Migration.IncludeTables,
		Exclude:          cfg.Migration.ExcludeTables,
		IncludeSequences: cfg.Migration.IncludeSequences,
	}

	s, err := extractor.Extract(ctx, cfg.Oracle.Schema, filter)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	plan := migration.BuildPlan(s, &cfg.Migration, cfg.Postgres.DropTarget)

	fmt.Println("-- existora2pg DDL preview")
	fmt.Println("-- Generated in dry-run mode")
	fmt.Println()

	for _, t := range plan.DDLOrder {
		fmt.Println(postgres.GenerateCreateTable(t, cfg.Postgres.Schema))

		if t.IsPartitioned() {
			bounds := migration.BuildPartitionBounds(t)
			for _, ddl := range postgres.GeneratePartitionDDL(t, cfg.Postgres.Schema, bounds) {
				fmt.Println(ddl)
			}
			fmt.Println()
		}
	}

	fmt.Println("-- Post-data constraints")
	for _, t := range plan.ConstraintOrder {
		if pk := postgres.GeneratePrimaryKey(t, cfg.Postgres.Schema); pk != "" {
			fmt.Println(pk)
		}
		for _, ddl := range postgres.GenerateIndexes(t, cfg.Postgres.Schema) {
			fmt.Println(ddl)
		}
		for _, ddl := range postgres.GenerateConstraints(t, cfg.Postgres.Schema) {
			fmt.Println(ddl)
		}
	}

	fmt.Println("\n-- Sequences")
	for _, seq := range plan.Sequences {
		fmt.Println(postgres.GenerateSequence(seq, cfg.Postgres.Schema))
	}

	return nil
}
