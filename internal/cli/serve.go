package cli

import (
	"fmt"
	"log/slog"

	"github.com/erenalidal/existora2pg/internal/logging"
	"github.com/erenalidal/existora2pg/pkg/api"
	"github.com/spf13/cobra"
)

var (
	servePort    int
	serveCORS    string
	serveStateDB string
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the web UI server",
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := logging.Setup("info", "text", "")
			if err != nil {
				return err
			}

			stateDB := serveStateDB
			if cfg != nil && cfg.Migration.StateDBPath != "" {
				stateDB = cfg.Migration.StateDBPath
			}

			srv, err := api.NewServer(api.ServerConfig{
				Port:        servePort,
				StateDBPath: stateDB,
				CORSOrigins: serveCORS,
				Logger:      l,
			})
			if err != nil {
				return fmt.Errorf("init server: %w", err)
			}

			l.Info("starting existora2pg web UI",
				slog.Int("port", servePort),
				slog.String("url", fmt.Sprintf("http://localhost:%d", servePort)))

			return srv.ListenAndServe()
		},
	}

	cmd.Flags().IntVar(&servePort, "port", 9740, "HTTP server port")
	cmd.Flags().StringVar(&serveCORS, "cors", "http://localhost:5173", "CORS allowed origins (comma separated)")
	cmd.Flags().StringVar(&serveStateDB, "state-db", "./existora2pg_state.db", "SQLite state database path")

	return cmd
}
