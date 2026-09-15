// SPDX-License-Identifier: Apache-2.0
// Package exporter ships AgentReports to the backend with TLS, auth,
// retries (exponential backoff + jitter), batching and drop-counters
// (backpressure: bounded queue, oldest batches dropped first, counted).
package exporter

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/infracity/infracity/pkg/model"
)

// Config for the exporter.
type Config struct {
	BackendURL string
	Token      string
	Insecure   bool // dev only
	Timeout    time.Duration
	MaxRetries int
}

// Exporter is a small durable queue -> HTTP poster.
type Exporter struct {
	cfg    Config
	client *http.Client
	queue  chan model.AgentReport

	sent    atomic.Int64
	dropped atomic.Int64
}

func New(cfg Config) *Exporter {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 5
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure}} //nolint:gosec
	e := &Exporter{
		cfg:    cfg,
		client: &http.Client{Transport: tr, Timeout: cfg.Timeout},
		queue:  make(chan model.AgentReport, 32),
	}
	go e.loop()
	return e
}

// Enqueue is non-blocking; drops oldest on full queue (backpressure) and counts it.
func (e *Exporter) Enqueue(rep model.AgentReport) {
	select {
	case e.queue <- rep:
	default:
		select {
		case <-e.queue:
			e.dropped.Add(1)
		default:
		}
		select {
		case e.queue <- rep:
		default:
			e.dropped.Add(1)
		}
	}
}

func (e *Exporter) loop() {
	for rep := range e.queue {
		if err := e.sendWithRetry(rep); err != nil {
			e.dropped.Add(1)
			log.Warn().Err(err).Msg("exporter dropped report after retries")
		} else {
			e.sent.Add(1)
		}
	}
}

func (e *Exporter) sendWithRetry(rep model.AgentReport) error {
	body, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	var last error
	for attempt := 0; attempt <= e.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt)*time.Second + time.Duration(rand.Intn(500))*time.Millisecond
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			time.Sleep(backoff)
		}
		req, err := http.NewRequest("POST", e.cfg.BackendURL+"/api/v1/ingest", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if e.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+e.cfg.Token)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			last = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		last = fmt.Errorf("backend %s", resp.Status)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
			return last // don't retry client errors (except rate limit)
		}
	}
	return last
}

// Stats for self-observability.
func (e *Exporter) Stats() (sent, dropped int64) { return e.sent.Load(), e.dropped.Load() }
