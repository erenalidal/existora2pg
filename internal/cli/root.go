package cli

import (
	"fmt"
	"os"

	"github.com/erenalidal/existora2pg/internal/config"
	"github.com/erenalidal/existora2pg/internal/logging"
	"github.com/spf13/cobra"
	"log/slog"
)

var (
	cfgFile string
	cfg     *config.Config
	logger  *slog.Logger
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "existora",
		Short: "Oracle to PostgreSQL migration tool",
		Long:  "existora2pg - Production-grade Oracle to PostgreSQL migration with partition support, resumability, and full observability.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip config loading for version and serve commands
			if cmd.Name() == "version" || cmd.Name() == "serve" {
				return nil
			}

			var err error
			cfg, err = config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			logger, err = logging.Setup(cfg.Logging.Level, cfg.Logging.Format, cfg.Logging.File)
			if err != nil {
				return fmt.Errorf("setup logger: %w", err)
			}

			return nil
		},
	}

	root.PersistentFlags().StringVarP(&cfgFile, "config", "c", "config.yaml", "config file path")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newExtractCmd())
	root.AddCommand(newMigrateCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newResumeCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newServeCmd())

	return root
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
