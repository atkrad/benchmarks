package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/runner"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("http2-idle", flag.ContinueOnError)
	configPath := fs.String("config", "", "defaults file")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: http2-idle --config defaults.json --out DIR [flags]")
		fmt.Fprintln(fs.Output(), "The Nix app sets the compiler paths. Run --help after --config is not required;")
		fmt.Fprintln(fs.Output(), "pass --help as the only argument to see the flags.")
	}
	// --config is consumed here. Every other flag, including --help, is
	// parsed against the defaults file so the usage text lists real flags.
	help := false
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			help = true
		case "--config":
			if i+1 >= len(args) {
				return errors.New("--config needs a path")
			}
			i++
			*configPath = args[i]
		default:
			rest = append(rest, args[i])
		}
	}
	if help {
		rest = append(rest, "--help")
	}
	if *configPath == "" {
		return errors.New("--config is required")
	}
	cfg, err := runner.Parse(rest, *configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runner.Run(ctx, cfg, runner.Deps{})
}
