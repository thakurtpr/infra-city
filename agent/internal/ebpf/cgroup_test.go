// SPDX-License-Identifier: Apache-2.0
package ebpf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerIDFromPath(t *testing.T) {
	id := strings.Repeat("a", 64)
	cases := map[string]string{
		"/sys/fs/cgroup/kubepods.slice/kubepods-burstable.slice/cri-containerd-" + id + ".scope": id,
		"/sys/fs/cgroup/system.slice/docker-" + id + ".scope":                                    id,
		"/sys/fs/cgroup/kubepods/pod11111111-2222-3333-4444-555555555555/" + id:                  id,
		"/sys/fs/cgroup/kubepods.slice":                                                          "",
		"/sys/fs/cgroup/init.scope":                                                              "",
	}
	for path, want := range cases {
		got, ok := ContainerIDFromPath(path)
		if want == "" && ok {
			t.Fatalf("%s: matched %q, want no match", path, got)
		}
		if want != "" && (!ok || got != want) {
			t.Fatalf("%s: got %q,%v want %q", path, got, ok, want)
		}
	}
}

func TestCgroupContainerIDsFakeTree(t *testing.T) {
	root := t.TempDir()
	id1, id2 := strings.Repeat("a", 64), strings.Repeat("b", 64)
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("kubepods.slice/cri-containerd-" + id1 + ".scope")
	mk("kubepods.slice/cri-containerd-" + id2 + ".scope")
	mk("kubepods.slice") // no ID: skipped
	mk("system.slice/init.scope")

	got, err := CgroupContainerIDs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("mapped = %d, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen[id1] || !seen[id2] {
		t.Fatalf("mapped ids = %v", seen)
	}
}

func TestCgroupContainerIDsEmptyRoot(t *testing.T) {
	got, err := CgroupContainerIDs("")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty root must map nothing, got %v %v", got, err)
	}
}

func TestResolveCgroupPods(t *testing.T) {
	cgroupInos := map[uint64]string{11: "ctr1", 22: "ctr2", 33: "unknown"}
	containers := map[string]string{"ctr1": "pod/c/ns/a", "ctr2": "pod/c/ns/b"}
	got := ResolveCgroupPods(cgroupInos, containers)
	if len(got) != 2 || got[11] != "pod/c/ns/a" || got[22] != "pod/c/ns/b" {
		t.Fatalf("resolved = %v", got)
	}
}

func TestCgroupRootOverride(t *testing.T) {
	t.Setenv("INFRACITY_CGROUPROOT", "/tmp/fake")
	if got := CgroupRoot(); got != "/tmp/fake" {
		t.Fatalf("root = %q", got)
	}
	t.Setenv("INFRACITY_CGROUPROOT", "")
	if got := CgroupRoot(); got != "/sys/fs/cgroup" {
		t.Fatalf("default root = %q", got)
	}
}
