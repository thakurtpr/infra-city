// SPDX-License-Identifier: Apache-2.0
// Command infracity-backend serves the control plane: ingestion, topology/graph,
// REST + WebSocket API, snapshots (time travel) and self-metrics.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/infracity/infracity/backend/internal/api"
	"github.com/infracity/infracity/backend/internal/graph"
	"github.com/infracity/infracity/backend/internal/store"
	"github.com/infracity/infracity/backend/internal/topology"
	"github.com/infracity/infracity/backend/internal/ws"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	authToken := flag.String("auth-token", "", "require Bearer token on /api/v1/ingest (empty = open, dev only)")
	demo := flag.Bool("demo", false, "seed synthetic demo city on boot")
	snapEvery := flag.Duration("snapshots-every", 60*time.Second, "snapshot interval for time travel")
	flag.Parse()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	g := graph.New()
	snaps := store.New(288)
	hub := ws.NewHub()
	srv := api.NewServer(g, snaps, hub, *authToken)

	if *demo || os.Getenv("INFRACITY_DEMO") == "1" {
		log.Info().Msg("seeding demo city")
		topology.SeedDemo(g)
	}

	// periodic snapshots for time travel
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		t := time.NewTicker(*snapEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				nodes, edges := g.Snapshot()
				snaps.Add(nodes, edges, "periodic")
			}
		}
	}()

	// demo traffic ticker: keeps the city alive without a cluster
	if *demo || os.Getenv("INFRACITY_DEMO") == "1" {
		go topology.DemoTrafficLoop(ctx, g, hub)
	}

	httpSrv := &http.Server{
		Addr:         *addr,
		Handler:      srv.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		log.Info().Str("addr", *addr).Msg("infracity backend listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("listen failed")
		}
	}()
	<-ctx.Done()
	log.Info().Msg("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
}
