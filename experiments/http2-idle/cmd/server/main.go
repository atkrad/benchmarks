package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/server"
)

func main() {
	if err := run(); err != nil {
		_ = protocol.Encode(os.Stdout, protocol.Error{Message: err.Error()})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("http2-idle-server", flag.ContinueOnError)
	allowGC := fs.Bool("allow-forced-gc", false, "enable POST /gc; off for the primary measurement")
	enablePprof := fs.Bool("enable-pprof", false, "enable the goroutine profile on the control listener")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := server.New(server.Options{AllowForcedGC: *allowGC, EnablePprof: *enablePprof})
	if err != nil {
		return err
	}
	if err := protocol.Encode(os.Stdout, protocol.Ready{
		HTTPSAddr:   srv.HTTPSAddr(),
		ControlAddr: srv.ControlAddr(),
		CertPEM:     string(srv.CertPEM()),
		GoVersion:   runtime.Version(),
	}); err != nil {
		return err
	}
	return srv.Serve(ctx)
}
