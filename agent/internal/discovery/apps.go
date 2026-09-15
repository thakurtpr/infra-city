// SPDX-License-Identifier: Apache-2.0
package discovery

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/infracity/infracity/pkg/model"
)

func (d *Discoverer) collectApps(ctx context.Context, nodes *[]model.Node, edges *[]model.Edge, add func(model.Node), now time.Time) {
	// Deployments
	deps, err := d.client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("list deployments failed")
	} else {
		for _, dep := range deps.Items {
			id := model.IDFor(model.TypeDeployment, d.clusterID, dep.Namespace, dep.Name)
			add(model.Node{ID: id, Type: model.TypeDeployment, Namespace: dep.Namespace, Name: dep.Name,
				Status: "running", Labels: dep.Labels, Version: imageTag(dep.Spec.Template.Spec.Containers),
				Metrics: &model.Metrics{Replicas: dep.Status.Replicas, ReadyReplicas: dep.Status.ReadyReplicas}})
		}
	}
	// DaemonSets
	ds, err := d.client.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, x := range ds.Items {
			add(model.Node{ID: model.IDFor(model.TypeDaemonSet, d.clusterID, x.Namespace, x.Name),
				Type: model.TypeDaemonSet, Namespace: x.Namespace, Name: x.Name, Status: "running", Labels: x.Labels})
		}
	}
	// StatefulSets
	ss, err := d.client.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, x := range ss.Items {
			add(model.Node{ID: model.IDFor(model.TypeStatefulSet, d.clusterID, x.Namespace, x.Name),
				Type: model.TypeStatefulSet, Namespace: x.Namespace, Name: x.Name, Status: "running", Labels: x.Labels,
				Role: dbRole(x.Name)})
		}
	}
	// Jobs + CronJobs
	jobs, err := d.client.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, x := range jobs.Items {
			add(model.Node{ID: model.IDFor(model.TypeJob, d.clusterID, x.Namespace, x.Name),
				Type: model.TypeJob, Namespace: x.Namespace, Name: x.Name, Status: "active"})
		}
	}
	cron, err := d.client.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, x := range cron.Items {
			add(model.Node{ID: model.IDFor(model.TypeCronJob, d.clusterID, x.Namespace, x.Name),
				Type: model.TypeCronJob, Namespace: x.Namespace, Name: x.Name, Status: "active"})
		}
	}
}

func (d *Discoverer) collectServices(ctx context.Context, nodes *[]model.Node, edges *[]model.Edge, add func(model.Node), now time.Time) {
	svcs, err := d.client.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("list services failed")
		return
	}
	for _, s := range svcs.Items {
		id := model.IDFor(model.TypeService, d.clusterID, s.Namespace, s.Name)
		ports := ""
		for _, p := range s.Spec.Ports {
			ports += itoa(int(p.Port)) + " "
		}
		add(model.Node{ID: id, Type: model.TypeService, Namespace: s.Namespace, Name: s.Name,
			Status: "running", Labels: s.Labels, IP: s.Spec.ClusterIP,
			Metadata: map[string]string{"ports": ports, "svcType": string(s.Spec.Type)}})
	}
	// EndpointSlices -> service->pod targets edges
	eps, err := d.client.DiscoveryV1().EndpointSlices("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, ep := range eps.Items {
			svc, ok := ep.Labels["kubernetes.io/service-name"]
			if !ok {
				continue
			}
			svcID := model.IDFor(model.TypeService, d.clusterID, ep.Namespace, svc)
			for _, e := range ep.Endpoints {
				if e.TargetRef != nil && e.TargetRef.Kind == "Pod" {
					podID := model.IDFor(model.TypePod, d.clusterID, ep.Namespace, e.TargetRef.Name)
					*edges = append(*edges, model.Edge{Source: svcID, Destination: podID, Type: model.EdgeTargets, UpdatedAt: now})
				}
			}
		}
	}
	// Ingresses
	ings, err := d.client.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, in := range ings.Items {
			id := model.IDFor(model.TypeIngress, d.clusterID, in.Namespace, in.Name)
			add(model.Node{ID: id, Type: model.TypeIngress, Namespace: in.Namespace, Name: in.Name, Status: "running"})
			for _, r := range in.Spec.Rules {
				if r.HTTP == nil {
					continue
				}
				for _, p := range r.HTTP.Paths {
					if p.Backend.Service != nil {
						*edges = append(*edges, model.Edge{
							Source: id, Destination: model.IDFor(model.TypeService, d.clusterID, in.Namespace, p.Backend.Service.Name),
							Type: model.EdgeRoutes, UpdatedAt: now})
					}
				}
			}
		}
	}
}

func (d *Discoverer) collectMeta(ctx context.Context, nodes *[]model.Node, add func(model.Node), now time.Time) {
	cms, err := d.client.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, c := range cms.Items {
			add(model.Node{ID: model.IDFor(model.TypeConfigMap, d.clusterID, c.Namespace, c.Name),
				Type: model.TypeConfigMap, Namespace: c.Namespace, Name: c.Name, Status: "active"})
		}
	}
	// Secrets: METADATA ONLY — never read .Data / .StringData.
	secs, err := d.client.CoreV1().Secrets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, s := range secs.Items {
			add(model.Node{ID: model.IDFor(model.TypeSecret, d.clusterID, s.Namespace, s.Name),
				Type: model.TypeSecret, Namespace: s.Namespace, Name: s.Name, Status: "active",
				Metadata: map[string]string{"secretType": string(s.Type), "keys": itoa(len(s.Data))}})
		}
	}
	pvcs, err := d.client.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, p := range pvcs.Items {
			add(model.Node{ID: model.IDFor(model.TypePVC, d.clusterID, p.Namespace, p.Name),
				Type: model.TypePVC, Namespace: p.Namespace, Name: p.Name, Status: string(p.Status.Phase)})
		}
	}
}

func imageTag(containers []corev1.Container) string {
	if len(containers) == 0 {
		return ""
	}
	img := containers[0].Image
	// return tag suffix after ':' if present
	for i := len(img) - 1; i >= 0; i-- {
		if img[i] == ':' {
			return img[i+1:]
		}
		if img[i] == '/' {
			break
		}
	}
	return ""
}

func dbRole(name string) string {
	for _, r := range []string{"postgres", "redis", "kafka", "elastic", "mongo"} {
		if containsFold(name, r) {
			return r
		}
	}
	return ""
}

func containsFold(hay, needle string) bool {
	if len(needle) > len(hay) {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := hay[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
