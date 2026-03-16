package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"text/tabwriter"

	"github.com/erenalidal/existora2pg/internal/oracle"
	"github.com/erenalidal/existora2pg/internal/postgres"
	"github.com/erenalidal/existora2pg/internal/validate"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate migration by comparing row counts and checksums",
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

			extractor := oracle.NewExtractor(oraDB, logger)
			reader := oracle.NewReader(oraDB, cfg.Migration.FetchSize, logger)
			writer := postgres.NewWriter(pgPool, cfg.Postgres.Schema, logger)

			filter := oracle.TableFilter{
				Include:          cfg.Migration.IncludeTables,
				Exclude:          cfg.Migration.ExcludeTables,
				IncludeSequences: cfg.Migration.IncludeSequences,
			}

			s, err := extractor.Extract(ctx, cfg.Oracle.Schema, filter)
			if err != nil {
				return fmt.Errorf("extract: %w", err)
			}

			v := validate.NewValidator(reader, writer, logger)

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "TABLE\tORACLE\tPOSTGRES\tMATCH\tERROR")
			fmt.Fprintln(w, "-----\t------\t--------\t-----\t-----")

			allMatch := true
			for _, t := range s.Tables {
				result, _ := v.ValidateRowCount(ctx, s.Owner, t.Name, "")
				match := "OK"
				if !result.Match {
					match = "MISMATCH"
					allMatch = false
				}
				errStr := result.Error
				if len(errStr) > 50 {
					errStr = errStr[:50] + "..."
				}
				fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\n",
					result.TableName, result.OracleCount, result.PGCount, match, errStr)
			}
			w.Flush()

			if !allMatch {
				return fmt.Errorf("validation failed: row count mismatches detected")
			}
			fmt.Println("\nAll tables validated successfully.")
			return nil
		},
	}
}
