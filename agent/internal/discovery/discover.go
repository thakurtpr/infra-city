// SPDX-License-Identifier: Apache-2.0
// Package discovery polls the Kubernetes API and normalizes resources into
// model.Nodes/Edges. Least-privilege RBAC: see helm/infracity/templates/rbac.yaml.
// Secret VALUES are never read — only metadata (name/namespace/keys count).
package discovery

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/infracity/infracity/pkg/model"
)

// Discoverer wraps a k8s client with cluster identity.
type Discoverer struct {
	client    kubernetes.Interface
	clusterID string
	nodeName  string // restrict pod listing to local node when set (DaemonSet efficiency)
}

// New returns a Discoverer; nodeName may be "" (all nodes, e.g. local dev).
func New(client kubernetes.Interface, clusterID, nodeName string) *Discoverer {
	return &Discoverer{client: client, clusterID: clusterID, nodeName: nodeName}
}

// Snapshot performs one full discovery pass.
func (d *Discoverer) Snapshot(ctx context.Context) ([]model.Node, []model.Edge, error) {
	now := time.Now()
	var nodes []model.Node
	var edges []model.Edge
	add := func(n model.Node) {
		n.Cluster = d.clusterID
		n.UpdatedAt = now
		if n.CreatedAt.IsZero() {
			n.CreatedAt = now
		}
		nodes = append(nodes, n)
	}

	nodes = append(nodes, model.Node{
		ID: model.IDFor(model.TypeCluster, d.clusterID, "", d.clusterID),
		Type: model.TypeCluster, Cluster: d.clusterID, Name: d.clusterID,
		Status: "running", UpdatedAt: now, CreatedAt: now,
	})

	// Nodes
	nodeList, err := d.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	for _, n := range nodeList.Items {
		status := "unknown"
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" {
				if c.Status == "True" {
					status = "ready"
				} else {
					status = "not-ready"
				}
			}
		}
		add(model.Node{
			ID: model.IDFor(model.TypeNode, d.clusterID, "", n.Name),
			Type: model.TypeNode, Name: n.Name, Status: status,
			Labels: n.Labels, Version: n.Status.NodeInfo.KubeletVersion,
			Metadata: map[string]string{"os": n.Status.NodeInfo.OSImage, "arch": n.Status.NodeInfo.Architecture},
		})
		edges = append(edges, model.Edge{
			Source: model.IDFor(model.TypeCluster, d.clusterID, "", d.clusterID),
			Destination: model.IDFor(model.TypeNode, d.clusterID, "", n.Name),
			Type: model.EdgeOwns, UpdatedAt: now,
		})
	}

	// Namespaces
	nsList, err := d.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	for _, ns := range nsList.Items {
		add(model.Node{
			ID: model.IDFor(model.TypeNamespace, d.clusterID, ns.Name, ns.Name),
			Type: model.TypeNamespace, Namespace: ns.Name, Name: ns.Name,
			Status: string(ns.Status.Phase), Labels: ns.Labels,
		})
	}

	// Pods (node-scoped when DaemonSet sets NODE_NAME)
	podOpts := metav1.ListOptions{}
	if d.nodeName != "" {
		podOpts.FieldSelector = "spec.nodeName=" + d.nodeName
	}
	podList, err := d.client.CoreV1().Pods("").List(ctx, podOpts)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range podList.Items {
		restarts := int32(0)
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
		}
		phase := string(p.Status.Phase)
		podID := model.IDFor(model.TypePod, d.clusterID, p.Namespace, p.Name)
		add(model.Node{
			ID: podID, Type: model.TypePod, Namespace: p.Namespace, Name: p.Name,
			Status: phase, Labels: p.Labels, NodeName: p.Spec.NodeName, IP: p.Status.PodIP,
			Owner: ownerRef(p.OwnerReferences),
			Metrics: &model.Metrics{Restarts: restarts},
		})
		if p.Spec.NodeName != "" {
			edges = append(edges, model.Edge{
				Source: podID, Destination: model.IDFor(model.TypeNode, d.clusterID, "", p.Spec.NodeName),
				Type: model.EdgeRunsOn, UpdatedAt: now,
			})
		}
	}

	// Deployments / DaemonSets / StatefulSets / Services / Ingresses / ConfigMaps / Secrets(meta) / PVCs / Jobs / CronJobs
	d.collectApps(ctx, &nodes, &edges, add, now)
	d.collectServices(ctx, &nodes, &edges, add, now)
	d.collectMeta(ctx, &nodes, add, now)

	log.Debug().Int("nodes", len(nodes)).Int("edges", len(edges)).Msg("discovery snapshot")
	return nodes, edges, nil
}

func ownerRef(refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[0].Kind + "/" + refs[0].Name
}
