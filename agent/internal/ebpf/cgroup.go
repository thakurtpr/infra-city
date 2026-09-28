// SPDX-License-Identifier: Apache-2.0
package ebpf

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// CgroupRoot returns the cgroupfs root to walk for inode->container mapping.
// Default is the host cgroupfs; INFRACITY_CGROUPROOT overrides it (tests,
// exotic layouts). Empty means "skip the walk" (unprivileged installs
// without the host mount).
func CgroupRoot() string {
	if v := os.Getenv("INFRACITY_CGROUPROOT"); v != "" {
		return v
	}
	return "/sys/fs/cgroup"
}

// containerIDRe matches runtimes' cgroup path conventions:
//   - systemd driver: .../cri-containerd-<hex>.scope, .../docker-<hex>.scope,
//     .../crio-<hex>.scope, .../libpod-<hex>.scope
//   - cgroupfs driver: .../kubepods/pod<uid>/<hex> (bare 64-hex leaf)
var containerIDRe = regexp.MustCompile(`(?:cri-containerd-|docker-|crio-|libpod-)?([0-9a-f]{64})(?:\.scope)?`)

// ContainerIDFromPath extracts a container ID from a cgroup path.
func ContainerIDFromPath(path string) (string, bool) {
	m := containerIDRe.FindStringSubmatch(path)
	if len(m) < 2 {
		return "", false
	}
	return m[1], true
}

// CgroupContainerIDs walks root mapping cgroup directory inode numbers to
// container IDs. kernfs inode numbers are host-global, so a BPF cgroup_id
// matches here without any PID-lifetime race. Directories without a
// recognizable container ID are skipped.
func CgroupContainerIDs(root string) (map[uint64]string, error) {
	out := map[uint64]string{}
	if root == "" {
		return out, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil // best-effort: skip unreadable subtrees
		}
		id, ok := ContainerIDFromPath(path)
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		out[stat.Ino] = id
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

// ResolveCgroupPods joins cgroup inodes to pod IDs via container IDs.
func ResolveCgroupPods(cgroupInos map[uint64]string, containers map[string]string) map[uint64]string {
	out := map[uint64]string{}
	for ino, ctr := range cgroupInos {
		if pod, ok := containers[ctr]; ok {
			out[ino] = pod
		}
	}
	return out
}
