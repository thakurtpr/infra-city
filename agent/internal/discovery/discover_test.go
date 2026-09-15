// SPDX-License-Identifier: Apache-2.0
package discovery

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/infracity/infracity/pkg/model"
)

func fakeClient() *fake.Clientset {
	podIP := "10.0.0.5"
	return fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "demo"}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api-abc", Namespace: "demo",
				Labels:          map[string]string{"app": "api"},
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-xyz"}},
			},
			Spec:   corev1.PodSpec{NodeName: "n1"},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: podIP},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "demo"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "api"}},
		},
		&discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api-1", Namespace: "demo",
				Labels: map[string]string{"kubernetes.io/service-name": "api"},
			},
			Endpoints: []discoveryv1.Endpoint{{
				TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "api-abc", Namespace: "demo"},
			}},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "demo"},
			// NOTE: fake secret carries a value here to prove the agent never reads it.
			Data: map[string][]byte{"password": []byte("super-secret-value")},
		},
	)
}

func TestSnapshotTopology(t *testing.T) {
	d := New(fakeClient(), "ci", "")
	nodes, edges, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]model.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, want := range []string{
		model.IDFor(model.TypeCluster, "ci", "", "ci"),
		model.IDFor(model.TypeNode, "ci", "", "n1"),
		model.IDFor(model.TypeNamespace, "ci", "demo", "demo"),
		model.IDFor(model.TypePod, "ci", "demo", "api-abc"),
		model.IDFor(model.TypeService, "ci", "demo", "api"),
		model.IDFor(model.TypeSecret, "ci", "demo", "db-creds"),
	} {
		if _, ok := byID[want]; !ok {
			t.Errorf("missing node %s", want)
		}
	}
	// pod -> node placement edge
	found := false
	for _, e := range edges {
		if e.Type == model.EdgeRunsOn && e.Source == model.IDFor(model.TypePod, "ci", "demo", "api-abc") {
			found = true
		}
	}
	if !found {
		t.Error("missing pod->node runs_on edge")
	}
	// service -> pod wiring from EndpointSlice
	found = false
	for _, e := range edges {
		if e.Type == model.EdgeTargets &&
			e.Source == model.IDFor(model.TypeService, "ci", "demo", "api") &&
			e.Destination == model.IDFor(model.TypePod, "ci", "demo", "api-abc") {
			found = true
		}
	}
	if !found {
		t.Error("missing service->pod targets edge")
	}
	// secret metadata only: key count recorded, value nowhere
	sec := byID[model.IDFor(model.TypeSecret, "ci", "demo", "db-creds")]
	if sec.Metadata["keys"] != "1" {
		t.Errorf("secret keys metadata = %q, want 1", sec.Metadata["keys"])
	}
	for _, n := range nodes {
		for k, v := range n.Labels {
			if v == "super-secret-value" {
				t.Fatalf("secret value leaked into node %s label %s", n.ID, k)
			}
		}
		if n.Metadata["password"] == "super-secret-value" {
			t.Fatalf("secret value leaked into node %s metadata", n.ID)
		}
	}
}
