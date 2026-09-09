// Command tbproxy caches TicketButler order data for the Cloud Native Denmark
// spreadsheet automation.
//
// Google Apps Script abandons an HTTP request after 60 seconds and offers no way to
// extend that. The TicketButler orders endpoint outgrew the limit, and it has no
// pagination or filtering to make the payload smaller, so the fetch had to move
// somewhere that can wait. This command is that somewhere: Cloud Scheduler triggers
// a refresh, the result is aggregated and stored, and Apps Script reads finished
// numbers in milliseconds.
//
//	tbproxy serve     run the HTTP API
//	tbproxy refresh   fetch, aggregate and store once, then exit
//	tbproxy verify    print the summary for a payload without storing anything
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/aggregate"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/config"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/httpapi"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/snapshot"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/store"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `tbproxy caches TicketButler order data for the Cloud Native Denmark sheets.

Usage:
  tbproxy serve              Run the HTTP API.
  tbproxy refresh            Fetch, aggregate and store one snapshot, then exit.
  tbproxy verify [-file f]   Print the summary for a payload. Stores nothing.
  tbproxy version            Print the version.

Configuration comes from the environment; see README.md.
`

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	// Cloud Run sends SIGTERM ahead of shutting an instance down; honouring it means
	// an in-flight refresh is cancelled cleanly instead of being killed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch command := os.Args[1]; command {
	case "serve":
		err = serve(ctx, log)
	case "refresh":
		err = refreshOnce(ctx, log)
	case "verify":
		err = verify(os.Args[2:], log)
	case "version":
		fmt.Println(version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", command, usage)
		os.Exit(2)
	}

	if err != nil {
		log.Error("exiting", "command", os.Args[1], "error", err)
		os.Exit(1)
	}
}

// build assembles the client, store and refresher from the environment.
func build(ctx context.Context, log *slog.Logger) (config.Config, *snapshot.Refresher, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, nil, err
	}

	closers := func() {}
	var snapshotStore store.Store
	if cfg.Bucket == "" {
		// No bucket means local development. It works, but the snapshot dies with
		// the process, so say so rather than letting someone deploy it by accident.
		log.Warn("no SNAPSHOT_BUCKET set; keeping the snapshot in memory only, which does not survive a restart")
		snapshotStore = store.NewMemory()
	} else {
		gcs, err := store.NewGCS(ctx, cfg.Bucket, cfg.Object)
		if err != nil {
			return config.Config{}, nil, nil, err
		}
		snapshotStore = gcs
		closers = func() { _ = gcs.Close() }
	}

	refresher := &snapshot.Refresher{
		Fetcher: &ticketbutler.Client{
			URL:     cfg.OrdersURL(),
			Token:   cfg.Token,
			File:    cfg.File,
			Timeout: cfg.UpstreamTimeout,
			Log:     log,
		},
		Store: snapshotStore,
		Options: aggregate.Options{
			MerchNamePatterns:    cfg.MerchNamePatterns,
			SponsorTicketTypePKs: cfg.SponsorTicketTypePKs,
		},
		Log: log,
	}
	return cfg, refresher, closers, nil
}

func serve(ctx context.Context, log *slog.Logger) error {
	cfg, refresher, closers, err := build(ctx, log)
	if err != nil {
		return err
	}
	defer closers()

	// Load whatever is already stored before accepting traffic, so the first read
	// after a cold start does not pay for a round trip to Cloud Storage.
	refresher.Warm(ctx)

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: httpapi.New(cfg, refresher, log, nil).Handler(),
		// Generous, because POST /v1/refresh legitimately runs for minutes. Cloud
		// Run's own request timeout is the real bound.
		ReadHeaderTimeout: 20 * time.Second,
		WriteTimeout:      cfg.UpstreamTimeout + time.Minute,
		ReadTimeout:       cfg.UpstreamTimeout + time.Minute,
	}

	errs := make(chan error, 1)
	go func() {
		log.Info("listening",
			"port", cfg.Port, "version", version,
			"store", refresher.Store.Describe(),
			"upstream_timeout", cfg.UpstreamTimeout.String(),
			"from_file", cfg.UsesFile())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func refreshOnce(ctx context.Context, log *slog.Logger) error {
	_, refresher, closers, err := build(ctx, log)
	if err != nil {
		return err
	}
	defer closers()

	snap, err := refresher.Refresh(ctx)
	if err != nil {
		return err
	}
	log.Info("refreshed once",
		"orders", snap.Summary.Counts.Orders, "tickets", snap.Summary.Counts.Tickets)
	return nil
}

// verify prints the summary for a payload without touching any store. It exists for
// the parity check: run it against a real payload and compare the numbers with what
// the spreadsheet currently shows, before letting the service near the sheets.
func verify(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	file := fs.String("file", "", "path to a TicketButler orders payload (defaults to TICKETBUTLER_FILE)")
	merch := fs.String("merch-patterns", "hoodie", "comma-separated lower-case substrings marking a ticket type as merchandise")
	sponsors := fs.String("sponsor-pks", "", "comma-separated sponsor ticket type ids")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *file
	if path == "" {
		path = os.Getenv("TICKETBUTLER_FILE")
	}
	if path == "" {
		return errors.New("verify needs -file or TICKETBUTLER_FILE")
	}

	opts, err := verifyOptions(*merch, *sponsors)
	if err != nil {
		return err
	}

	client := &ticketbutler.Client{File: path, Log: log}
	result, err := client.FetchOrders(context.Background())
	if err != nil {
		return err
	}

	out := struct {
		Summary  aggregate.Summary   `json:"summary"`
		Sponsors []aggregate.Sponsor `json:"sponsors"`
	}{
		Summary:  aggregate.Summarise(result.Orders, opts),
		Sponsors: aggregate.Sponsors(result.Orders, opts),
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func verifyOptions(merch, sponsors string) (aggregate.Options, error) {
	pks, err := config.ParseIntList(sponsors)
	if err != nil {
		return aggregate.Options{}, fmt.Errorf("-sponsor-pks: %w", err)
	}
	return aggregate.Options{
		MerchNamePatterns:    config.ParseMerchPatterns(merch),
		SponsorTicketTypePKs: pks,
	}, nil
}
