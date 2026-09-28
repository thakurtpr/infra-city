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
	graphTTL := flag.Duration("graph-ttl", 5*time.Minute, "evict graph entries not refreshed within this long (0 disables)")
	postgresDSN := flag.String("postgres-dsn", "", "durable snapshot log DSN (empty = ring only); or INFRACITY_POSTGRES_DSN")
	flag.Parse()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	g := graph.New()
	snaps := store.New(288)
	dsn := *postgresDSN
	if dsn == "" {
		dsn = os.Getenv("INFRACITY_POSTGRES_DSN")
	}
	if dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		be, err := store.NewPostgresBackend(ctx, dsn)
		cancel()
		if err != nil {
			log.Fatal().Err(err).Msg("postgres snapshot backend configured but unreachable")
		}
		snaps = store.NewWithBackend(288, be)
		defer func() { _ = snaps.Close() }()
	}
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

	// graph GC: drop edges/nodes no report refreshed within TTL, so dead
	// flows and deleted workloads stop haunting the city (and memory).
	// Snapshots already taken keep history for time travel.
	if *graphTTL > 0 {
		go func() {
			interval := *graphTTL / 2
			if interval < 10*time.Second {
				interval = 10 * time.Second
			}
			if interval > 5*time.Minute {
				interval = 5 * time.Minute
			}
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					n, e := srv.EvictOlderThan(time.Now().Add(-*graphTTL))
					log.Debug().Int("nodes", n).Int("edges", e).Msg("graph eviction")
				}
			}
		}()
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
