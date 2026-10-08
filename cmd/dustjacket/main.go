// dustjacket is the whole server in one binary: on Lambda it serves the
// function URL; locally it serves HTTP.
//
//	dustjacket                          # Lambda entry point (AWS_LAMBDA_RUNTIME_API set)
//	dustjacket serve [-addr :8080] [-memory]
//
// Configuration is DUSTJACKET_* environment variables (config.go).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx := context.Background()
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" && len(os.Args) == 1 {
		runLambda(ctx)
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: dustjacket serve [-addr :8080] [-memory]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	memory := fs.Bool("memory", false, "in-memory store and local sealer; no AWS")
	_ = fs.Parse(os.Args[2:])
	handler, cfg, err := build(ctx, *memory)
	if err != nil {
		fatal(err)
	}
	slog.Info("listening", "addr", *addr, "base_url", cfg.BaseURL, "memory", *memory)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	slog.Error("fatal", "err", err)
	os.Exit(1)
}

// budgetMargin is how long before Lambda's deadline a request's own work
// stops: long enough to log and answer, which a Lambda timeout would cut
// off.
const budgetMargin = 3 * time.Second

// budget is the context a request works under: Lambda's deadline less
// budgetMargin, so every call that honors its context (Hardcover, the AWS
// SDK) gives up first.
func budget(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, deadline.Add(-budgetMargin))
}

func runLambda(ctx context.Context) {
	handler, _, err := build(ctx, false)
	if err != nil {
		fatal(err)
	}
	adapter := httpadapter.NewV2(handler)
	lambda.Start(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		ctx, stop := budget(ctx)
		defer stop()
		return adapter.ProxyWithContext(ctx, req)
	})
}
