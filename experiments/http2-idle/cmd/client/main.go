package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/client"
	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
)

func main() {
	if err := run(); err != nil {
		_ = protocol.Encode(os.Stdout, protocol.Error{Message: err.Error()})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("http2-idle-client", flag.ContinueOnError)
	url := fs.String("url", "", "workload URL")
	certPath := fs.String("cert", "", "PEM file the client should trust")
	n := fs.Int("n", 0, "idle connections to hold")
	dial := fs.Int("dial-concurrency", 1, "parallel dials")
	retries := fs.Int("max-retries", 0, "dial retries per connection")
	name := fs.String("server-name", "localhost", "TLS server name")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	certPEM, err := os.ReadFile(*certPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *n > 0 {
		_ = protocol.Encode(os.Stdout, protocol.Progress{Established: 0, Wanted: *n})
	}
	session, err := client.Dial(ctx, client.Config{
		URL:             *url,
		CertPEM:         certPEM,
		ServerName:      *name,
		Connections:     *n,
		DialConcurrency: *dial,
		MaxRetries:      *retries,
	})
	if err != nil {
		return err
	}
	defer session.Close()
	if err := protocol.Encode(os.Stdout, protocol.Ready{
		Connections: *n,
		Retries:     session.Retries,
		Established: *n,
	}); err != nil {
		return err
	}

	state := ""
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		msg, err := protocol.Decode(scanner.Bytes())
		if err != nil {
			return err
		}
		next, err := protocol.AdvanceCommand(state, protocol.EventOf(msg))
		if err != nil {
			return err
		}
		state = next
		switch msg.(type) {
		case *protocol.Recheck:
			result, recErr := session.Recheck(ctx, *url, 10*time.Second)
			out := protocol.RecheckResult{
				OK:       result.OK,
				Reused:   result.Reused,
				Replaced: result.Replaced,
				Errors:   result.Errors,
				Locals:   result.Locals,
			}
			if recErr != nil {
				out.Detail = recErr.Error()
			}
			if err := protocol.Encode(os.Stdout, out); err != nil {
				return err
			}
			if recErr != nil {
				return recErr
			}
		case *protocol.Close:
			if err := protocol.Encode(os.Stdout, protocol.Closed{}); err != nil {
				return err
			}
			return nil
		default:
			return fmt.Errorf("unexpected command %T", msg)
		}
	}
	return scanner.Err()
}
