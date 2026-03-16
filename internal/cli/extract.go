package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/erenalidal/existora2pg/internal/oracle"
	"github.com/erenalidal/existora2pg/internal/schema"
	"github.com/spf13/cobra"
)

var extractOutput string

func newExtractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract Oracle schema metadata and print migration plan",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			logger.Info("connecting to Oracle", "dsn", maskDSN(cfg.Oracle.DSN))

			db, err := oracle.OpenPool(ctx, cfg.Oracle.DSN, cfg.Oracle.MaxConns)
			if err != nil {
				return fmt.Errorf("connect oracle: %w", err)
			}
			defer db.Close()

			extractor := oracle.NewExtractor(db, logger)
			filter := oracle.TableFilter{
				Include:          cfg.Migration.IncludeTables,
				Exclude:          cfg.Migration.ExcludeTables,
				IncludeSequences: cfg.Migration.IncludeSequences,
			}

			s, err := extractor.Extract(ctx, cfg.Oracle.Schema, filter)
			if err != nil {
				return fmt.Errorf("extract: %w", err)
			}

			printSchemaSummary(s)

			if extractOutput != "" {
				return saveSchemaJSON(s, extractOutput)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&extractOutput, "output", "o", "", "save schema metadata as JSON to file")

	return cmd
}

func printSchemaSummary(s *schema.Schema) {
	fmt.Printf("\nSchema: %s\n", s.Owner)
	fmt.Printf("Tables: %d\n", len(s.Tables))
	fmt.Printf("Sequences: %d\n\n", len(s.Sequences))

	for _, t := range s.Tables {
		partInfo := ""
		if t.IsPartitioned() {
			partInfo = fmt.Sprintf(" [partitioned: %s, %d partitions]",
				t.Partitioning.Strategy, len(t.Partitioning.Partitions))
		}

		fkCount := 0
		idxCount := len(t.Indexes)
		for _, c := range t.Constraints {
			if c.Type == schema.ConstraintFK {
				fkCount++
			}
		}

		fmt.Printf("  %-30s %3d cols  %2d idx  %d FK%s\n",
			t.Name, len(t.Columns), idxCount, fkCount, partInfo)
	}
	fmt.Println()
}

func saveSchemaJSON(s *schema.Schema, path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
