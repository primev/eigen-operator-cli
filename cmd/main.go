package main

import (
	registration "eigen-operator-cli/pkg/registration"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	eigenclitypes "github.com/Layr-Labs/eigenlayer-cli/pkg/types"
	"github.com/primev/mev-commit/x/util"
	"github.com/urfave/cli/v2"
	"github.com/urfave/cli/v2/altsrc"
	"gopkg.in/yaml.v3"
)

var (
	optionOperatorConfig = altsrc.NewStringFlag(&cli.StringFlag{
		Name:     "operator-config",
		Usage:    "Path to operator.yml config file",
		EnvVars:  []string{"OPERATOR_CONFIG"},
		Required: true,
	})

	optionAVSAddress = altsrc.NewStringFlag(&cli.StringFlag{
		Name:     "avs-address",
		Usage:    "Address of the mev-commit AVS contract",
		EnvVars:  []string{"AVS_ADDRESS"},
		Required: true,
	})

	optionBoostGasParams = altsrc.NewStringFlag(&cli.StringFlag{
		Name:     "boost-gas-params",
		Usage:    "Whether to boost gas params to speed up tx inclusion",
		EnvVars:  []string{"BOOST_GAS_PARAMS"},
		Required: true,
	})

	optionKeystorePassword = altsrc.NewStringFlag(&cli.StringFlag{
		Name:     "keystore-password",
		Usage:    "Password for the keystore",
		EnvVars:  []string{"KEYSTORE_PASSWORD"},
		Required: false,
	})

	optionLogLevel = altsrc.NewStringFlag(&cli.StringFlag{
		Name:    "log-level",
		Usage:   "Log level, options are 'debug', 'info', 'warn', 'error'",
		EnvVars: []string{"LOG_LEVEL"},
		Value:   "info",
		Action: func(_ *cli.Context, s string) error {
			if !slices.Contains([]string{"debug", "info", "warn", "error"}, s) {
				return fmt.Errorf("invalid value: -log-level=%q", s)
			}
			return nil
		},
	})

	optionLogFmt = altsrc.NewStringFlag(&cli.StringFlag{
		Name:    "log-fmt",
		Usage:   "Log format, options are 'text' or 'json'",
		EnvVars: []string{"LOG_FMT"},
		Value:   "text",
		Action: func(_ *cli.Context, s string) error {
			if !slices.Contains([]string{"text", "json"}, s) {
				return fmt.Errorf("invalid value: -log-fmt=%q", s)
			}
			return nil
		},
	})

	optionLogTags = altsrc.NewStringFlag(&cli.StringFlag{
		Name:    "log-tags",
		Usage:   "Log tags is a comma-separated list of <name:value> pairs that will be inserted into each log line",
		EnvVars: []string{"LOG_TAGS"},
		Action: func(ctx *cli.Context, s string) error {
			for i, p := range strings.Split(s, ",") {
				if len(strings.Split(p, ":")) != 2 {
					return fmt.Errorf("invalid value at index %d: -log-tags=%q", i, s)
				}
			}
			return nil
		},
	})
)

func readConfig(file string) (eigenclitypes.OperatorConfig, error) {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return eigenclitypes.OperatorConfig{}, fmt.Errorf("eigen config file not found: %s", file)
	}

	bz, err := os.ReadFile(file)
	if err != nil {
		return eigenclitypes.OperatorConfig{}, fmt.Errorf("read eigen config file: %w", err)
	}

	var config eigenclitypes.OperatorConfig
	if err := yaml.Unmarshal(bz, &config); err != nil {
		return eigenclitypes.OperatorConfig{}, fmt.Errorf("unmarshal eigen config file: %w", err)
	}

	if !filepath.IsAbs(config.PrivateKeyStorePath) {
		absPath, err := filepath.Abs(config.PrivateKeyStorePath)
		if err != nil {
			return eigenclitypes.OperatorConfig{}, fmt.Errorf("get absolute path: %w", err)
		}
		config.PrivateKeyStorePath = absPath
	}

	return config, nil
}

func main() {
	flags := []cli.Flag{
		optionOperatorConfig,
		optionAVSAddress,
		optionBoostGasParams,
		optionKeystorePassword,
		optionLogLevel,
		optionLogFmt,
		optionLogTags,
	}

	app := &cli.App{
		Name:  "mev-commit-operator-cli",
		Usage: "CLI for mev-commit AVS operator registration.",
		Commands: []*cli.Command{
			{
				Name:   "register",
				Usage:  "Register an operator",
				Flags:  flags,
				Action: newAction((*registration.Command).RegisterOperator),
			},
			{
				Name:   "request-deregistration",
				Usage:  "Request operator deregistration",
				Flags:  flags,
				Action: newAction((*registration.Command).RequestOperatorDeregistration),
			},
			{
				Name:   "deregister",
				Usage:  "Deregister an operator",
				Flags:  flags,
				Action: newAction((*registration.Command).DeregisterOperator),
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(app.ErrWriter, err)
	}
}

func newAction(action func(*registration.Command, *cli.Context) error) cli.ActionFunc {
	return func(ctx *cli.Context) error {
		logger, err := util.NewLogger(
			ctx.String(optionLogLevel.Name),
			ctx.String(optionLogFmt.Name),
			ctx.String(optionLogTags.Name),
			ctx.App.Writer,
		)
		if err != nil {
			logger.Error("failed to create logger", "error", err)
			return err
		}
		operConfig, err := readConfig(ctx.String(optionOperatorConfig.Name))
		if err != nil {
			logger.Error("failed to read operator config", "error", err)
			return err
		}
		if err := action(&registration.Command{
			Logger:              logger,
			OperatorConfig:      &operConfig,
			KeystorePassword:    ctx.String(optionKeystorePassword.Name),
			MevCommitAVSAddress: ctx.String(optionAVSAddress.Name),
			BoostGasParams:      ctx.Bool(optionBoostGasParams.Name),
		}, ctx); err != nil {
			logger.Error("command execution failed")
			return err
		}
		return nil
	}
}
