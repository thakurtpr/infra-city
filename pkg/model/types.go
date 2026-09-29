// SPDX-License-Identifier: Apache-2.0
// Package model defines the normalized InfraCity data model.
// Every infrastructure object is a Node; every relationship/traffic is an Edge.
// This is the single source of truth shared by agent, backend and frontend.
package model

import "time"

// Resource types (extensible — do not hard-code vendor tech into architecture).
const (
	TypeCluster     = "cluster"
	TypeNode        = "node"
	TypeNamespace   = "namespace"
	TypeDeployment  = "deployment"
	TypeDaemonSet   = "daemonset"
	TypeStatefulSet = "statefulset"
	TypeReplicaSet  = "replicaset"
	TypePod         = "pod"
	TypeContainer   = "container"
	TypeService     = "service"
	TypeEndpoint    = "endpoint"
	TypeIngress     = "ingress"
	TypeGateway     = "gateway"
	TypeHTTPRoute   = "httproute"
	TypeConfigMap   = "configmap"
	TypeSecret      = "secret" // metadata only, never values
	TypePV          = "pv"
	TypePVC         = "pvc"
	TypeJob         = "job"
	TypeCronJob     = "cronjob"
	TypeExternal    = "external" // cloud LB, managed DB, SaaS, etc.
	TypeDatabase    = "database" // logical DB role (postgres/redis/kafka/...)
)

// Edge types.
const (
	EdgeOwns    = "owns"    // cluster->node, ns->deployment, deploy->pod ...
	EdgeTargets = "targets" // service -> pod (selector/endpointslice)
	EdgeRoutes  = "routes"  // ingress/gateway -> service
	EdgeRunsOn  = "runs_on" // pod -> node
	EdgeNetwork = "network" // observed L4/L7 traffic
	EdgeDepends = "depends" // inferred dependency (from traffic)
	EdgeMounts  = "mounts"  // pod -> configmap/secret/pvc
)

// Node is any infrastructure object with a globally unique ID:
//
//	"<type>/<cluster>/<namespace>/<name>" (namespace omitted when cluster-scoped)
type Node struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Cluster     string            `json:"cluster"`
	Namespace   string            `json:"namespace,omitempty"`
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Status      string            `json:"status,omitempty"`
	Kind        string            `json:"kind,omitempty"` // k8s kind
	Version     string            `json:"version,omitempty"`
	Role        string            `json:"role,omitempty"` // e.g. database role: postgres|redis|kafka|s3|elastic
	Owner       string            `json:"owner,omitempty"`
	NodeName    string            `json:"nodeName,omitempty"`
	IP          string            `json:"ip,omitempty"`
	CreatedAt   time.Time         `json:"createdAt,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Metrics     *Metrics          `json:"metrics,omitempty"`
}

// Metrics attached to a node (point-in-time; history lives in the TS store).
type Metrics struct {
	CPUCores      float64 `json:"cpuCores,omitempty"`
	CPUPct        float64 `json:"cpuPct,omitempty"`
	MemBytes      int64   `json:"memBytes,omitempty"`
	MemPct        float64 `json:"memPct,omitempty"`
	Replicas      int32   `json:"replicas,omitempty"`
	ReadyReplicas int32   `json:"readyReplicas,omitempty"`
	Restarts      int32   `json:"restarts,omitempty"`
	ReqPerSec     float64 `json:"reqPerSec,omitempty"`
	ErrRate       float64 `json:"errRate,omitempty"` // 0..1
	LatencyMsP50  float64 `json:"latencyMsP50,omitempty"`
	LatencyMsP95  float64 `json:"latencyMsP95,omitempty"`
	BytesPerSec   float64 `json:"bytesPerSec,omitempty"`
	CostPerMonth  float64 `json:"costPerMonth,omitempty"`
}

// Edge is a directed relationship or observed flow between two nodes.
type Edge struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	Destination    string    `json:"destination"`
	Type           string    `json:"type"`
	Protocol       string    `json:"protocol,omitempty"` // TCP/UDP/HTTP/gRPC/...
	SrcPort        int       `json:"srcPort,omitempty"`
	DstPort        int       `json:"dstPort,omitempty"`
	RequestsPerSec float64   `json:"requestsPerSec,omitempty"`
	LatencyMs      float64   `json:"latencyMs,omitempty"`
	BytesPerSec    float64   `json:"bytesPerSec,omitempty"`
	ErrorsPerSec   float64   `json:"errorsPerSec,omitempty"`
	Connections    int64     `json:"connections,omitempty"`
	Confidence     float64   `json:"confidence,omitempty"` // dependency inference 0..1
	Allowed        *bool     `json:"allowed,omitempty"`    // security mode: nil=unknown
	UpdatedAt      time.Time `json:"updatedAt,omitempty"`
}

// Event is a discrete occurrence streamed over the WebSocket API.
type Event struct {
	Type      string            `json:"type"` // RESOURCE_CREATED|UPDATED|DELETED|NETWORK_FLOW|METRIC_UPDATE|...
	Resource  *Node             `json:"resource,omitempty"`
	Edge      *Edge             `json:"edge,omitempty"`
	Message   string            `json:"message,omitempty"`
	Severity  string            `json:"severity,omitempty"` // info|warn|critical
	Timestamp time.Time         `json:"timestamp"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// Event types for the WS stream.
const (
	EvResourceCreated    = "RESOURCE_CREATED"
	EvResourceUpdated    = "RESOURCE_UPDATED"
	EvResourceDeleted    = "RESOURCE_DELETED"
	EvNetworkFlow        = "NETWORK_FLOW"
	EvMetricUpdate       = "METRIC_UPDATE"
	EvPodRestart         = "POD_RESTART"
	EvDeploymentStarted  = "DEPLOYMENT_STARTED"
	EvDeploymentProgress = "DEPLOYMENT_PROGRESS"
	EvDeploymentDone     = "DEPLOYMENT_COMPLETED"
	EvIncidentStarted    = "INCIDENT_STARTED"
	EvIncidentResolved   = "INCIDENT_RESOLVED"
	EvChangeDetected     = "CHANGE_DETECTED"
)

// Snapshot is a point-in-time copy of the graph for Time Travel.
type Snapshot struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
	Label     string    `json:"label,omitempty"`
}

// AgentReport is what each DaemonSet agent POSTs to /api/v1/ingest.
type AgentReport struct {
	ClusterID string     `json:"clusterId"`
	NodeName  string     `json:"nodeName"`
	Timestamp int64      `json:"timestamp"` // unix nano
	Nodes     []Node     `json:"nodes"`
	Edges     []Edge     `json:"edges"`
	Events    []Event    `json:"events,omitempty"`
	Stats     AgentStats `json:"stats"`
}

// AgentStats feeds "observability of InfraCity itself".
type AgentStats struct {
	EventsPerSec  float64 `json:"eventsPerSec"`
	FlowsPerSec   float64 `json:"flowsPerSec"`
	DroppedEvents int64   `json:"droppedEvents"`
	CPUCores      float64 `json:"cpuCores"`
	MemBytes      int64   `json:"memBytes"`
	EBPFEnabled   bool    `json:"ebpfEnabled"`
}

// ClusterMeta is shown in the cluster picker / command center.
type ClusterMeta struct {
	ID          string `json:"id"`
	Environment string `json:"environment,omitempty"`
	Region      string `json:"region,omitempty"`
	Provider    string `json:"provider,omitempty"`
	K8sVersion  string `json:"k8sVersion,omitempty"`
	NodeCount   int    `json:"nodeCount"`
	PodCount    int    `json:"podCount"`
	Health      string `json:"health"` // healthy|degraded|critical
}

// AgentStatus is the last report heard from a cluster's agent(s),
// surfaced at /api/self. Self-observability for the observers.
type AgentStatus struct {
	ClusterID     string  `json:"clusterId"`
	EBPFEnabled   bool    `json:"ebpfEnabled"`
	FlowsPerSec   float64 `json:"flowsPerSec,omitempty"`
	EventsPerSec  float64 `json:"eventsPerSec,omitempty"`
	DroppedEvents int64   `json:"droppedEvents,omitempty"`
	LastSeen      int64   `json:"lastSeen"` // unix nano
}

// Dependency is an inferred service dependency with confidence.
type Dependency struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Confidence float64 `json:"confidence"`
	ReqPerSec  float64 `json:"reqPerSec"`
	LatencyMs  float64 `json:"latencyMs"`
	ErrRate    float64 `json:"errRate"`
}

// Incident is a detected or injected failure with blast radius.
type Incident struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	RootNode    string   `json:"rootNode"`
	Status      string   `json:"status"` // firing|resolved
	Severity    string   `json:"severity"`
	StartedAt   int64    `json:"startedAt"`
	ResolvedAt  *int64   `json:"resolvedAt,omitempty"`
	Affected    []string `json:"affected"`
	Description string   `json:"description,omitempty"`
}

// Change is a detected infra change for "What changed?" mode.
type Change struct {
	ID        string `json:"id"`
	Timestamp int64  `json:"timestamp"`
	Kind      string `json:"kind"` // added|removed|modified|scale|deploy|dependency
	Summary   string `json:"summary"`
	NodeID    string `json:"nodeId,omitempty"`
}

// IDFor builds a canonical node ID.
func IDFor(typ, cluster, namespace, name string) string {
	if namespace == "" {
		return typ + "/" + cluster + "/" + name
	}
	return typ + "/" + cluster + "/" + namespace + "/" + name
}
