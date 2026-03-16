package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"text/tabwriter"

	"github.com/erenalidal/existora2pg/internal/jobstore"
	"github.com/spf13/cobra"

	_ "modernc.org/sqlite"
)

var statusTable string

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show migration job status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			store, err := jobstore.New(cfg.Migration.StateDBPath)
			if err != nil {
				return fmt.Errorf("open state db: %w", err)
			}
			defer store.Close()

			if err := store.InitSchema(ctx); err != nil {
				return fmt.Errorf("init state schema: %w", err)
			}

			runID, err := store.GetLatestRunID(ctx)
			if err != nil {
				return fmt.Errorf("no migration runs found: %w", err)
			}

			// Print summary
			summary, err := store.GetRunSummary(ctx, runID)
			if err != nil {
				return err
			}

			fmt.Printf("Run: %s\n", runID)
			fmt.Printf("Total: %d | Completed: %d | Failed: %d | Pending: %d | Running: %d\n\n",
				summary.Total, summary.Completed, summary.Failed, summary.Pending, summary.Running)

			// Print job details
			jobs, err := store.GetJobs(ctx, runID, statusTable)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "TABLE\tPARTITION\tPHASE\tSTATE\tROWS\tERROR")
			fmt.Fprintln(w, "-----\t---------\t-----\t-----\t----\t-----")

			for _, j := range jobs {
				errMsg := j.Error
				if len(errMsg) > 60 {
					errMsg = errMsg[:60] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
					j.TableName, j.Partition, j.Phase, j.State, j.RowsCopied, errMsg)
			}
			w.Flush()

			return nil
		},
	}

	cmd.Flags().StringVar(&statusTable, "table", "", "filter by table name")

	return cmd
}
