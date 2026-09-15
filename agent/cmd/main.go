// Command infracity-agent runs as a DaemonSet: discovers local Kubernetes state,
// samples network flows (eBPF when privileged, /proc fallback otherwise) and
// ships normalized AgentReports to the backend.
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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/infracity/infracity/agent/internal/discovery"
	"github.com/infracity/infracity/agent/internal/ebpf"
	"github.com/infracity/infracity/agent/internal/exporter"
	"github.com/infracity/infracity/agent/internal/netmon"
	"github.com/infracity/infracity/pkg/model"
)

func main() {
	backend := flag.String("backend", envOr("INFRACITY_BACKEND", "http://localhost:8080"), "backend base URL")
	token := flag.String("token", os.Getenv("INFRACITY_TOKEN"), "cluster auth token")
	cluster := flag.String("cluster", envOr("INFRACITY_CLUSTER", "dev"), "cluster identity (unique per cluster)")
	nodeName := flag.String("node", os.Getenv("NODE_NAME"), "restrict discovery to this node (DaemonSet)")
	interval := flag.Duration("interval", 15*time.Second, "discovery interval")
	insecure := flag.Bool("insecure", os.Getenv("INFRACITY_INSECURE") == "1", "skip TLS verify (dev only)")
	healthAddr := flag.String("health-addr", ":8081", "health/metrics listen addr")
	flag.Parse()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Info().Str("cluster", *cluster).Str("backend", *backend).Msg("infracity agent starting")

	cfg := kubeConfig()
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("k8s client failed (are you in a cluster? use --help; dev hint: make dev uses backend demo mode)")
	}
	disc := discovery.New(client, *cluster, *nodeName)
	tracer := ebpf.New()
	defer tracer.Close()
	log.Info().Bool("ebpf", tracer.Enabled()).Str("netmode", tracer.Reason()).Msg("network observer ready")

	exp := exporter.New(exporter.Config{BackendURL: *backend, Token: *token, Insecure: *insecure})

	go serveHealth(*healthAddr, tracer)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	t := time.NewTicker(*interval)
	defer t.Stop()
	runOnce(ctx, disc, tracer, exp, *cluster, *nodeName)
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("agent shutting down")
			return
		case <-t.C:
			runOnce(ctx, disc, tracer, exp, *cluster, *nodeName)
		}
	}
}

func runOnce(ctx context.Context, disc *discovery.Discoverer, tracer ebpf.Tracer, exp *exporter.Exporter, cluster, nodeName string) {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	nodes, edges, err := disc.Snapshot(cctx)
	if err != nil {
		log.Warn().Err(err).Msg("discovery failed")
		return
	}
	// network flows
	flows, ferr := tracer.Sample()
	if ferr != nil {
		log.Debug().Err(ferr).Msg("flow sample failed")
	} else {
		ipToPod := map[string]string{}
		for _, n := range nodes {
			if n.Type == model.TypePod && n.IP != "" {
				ipToPod[n.IP] = n.ID
			}
		}
		edges = append(edges, netmon.ToEdges(flows, ipToPod, cluster)...)
	}
	sent, dropped := exp.Stats()
	rep := model.AgentReport{
		ClusterID: cluster, NodeName: nodeName, Timestamp: time.Now().UnixNano(),
		Nodes: nodes, Edges: edges,
		Stats: model.AgentStats{
			EventsPerSec: float64(len(nodes)+len(edges)) / time.Since(start).Seconds(),
			FlowsPerSec:  float64(len(flows)),
			DroppedEvents: dropped, EBPFEnabled: tracer.Enabled(),
		},
	}
	_ = sent
	exp.Enqueue(rep)
	log.Info().Int("nodes", len(nodes)).Int("edges", len(edges)).Int("flows", len(flows)).Dur("dur", time.Since(start)).Msg("report queued")
}

func serveHealth(addr string, tracer ebpf.Tracer) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}`)) })
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ready":true}`)) })
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# infracity agent\ninfracity_agent_ebpf_enabled " + boolNum(tracer.Enabled()) + "\n"))
	})
	_ = http.ListenAndServe(addr, mux) //nolint:gosec
}

func kubeConfig() *rest.Config {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err == nil {
			return cfg
		}
		log.Warn().Err(err).Msg("KUBECONFIG failed, trying in-cluster")
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg
	}
	// last resort: default kubeconfig path
	cfg, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		log.Fatal().Err(err).Msg("no kubeconfig found")
	}
	return cfg
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func boolNum(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
