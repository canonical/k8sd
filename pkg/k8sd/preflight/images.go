package preflight

import (
	"os"
	"path/filepath"
	"strings"
)

// componentMapping maps image repo sub-paths to component info.
type componentMapping struct {
	Name    string
	RepoURL string
}

// componentMappings maps recognizable sub-paths in image URLs to component metadata.
// Matches against the full image path using strings.Contains.
var componentMappings = map[string]componentMapping{
	"canonical/cilium-operator":           {Name: "cilium-operator", RepoURL: "https://github.com/cilium/cilium"},
	"canonical/cilium":                    {Name: "cilium", RepoURL: "https://github.com/cilium/cilium"},
	"canonical/coredns":                   {Name: "coredns", RepoURL: "https://github.com/coredns/coredns"},
	"canonical/metrics-server":            {Name: "metrics-server", RepoURL: "https://github.com/kubernetes-sigs/metrics-server"},
	"canonical/rawfile-localpv":           {Name: "localpv", RepoURL: "https://github.com/kubernetes-sigs/sig-storage-local-static-provisioner"},
	"canonical/csi-node-driver-registrar": {Name: "csi-node-driver", RepoURL: "https://github.com/kubernetes-csi/node-driver-registrar"},
	"canonical/csi-provisioner":           {Name: "csi-provisioner", RepoURL: "https://github.com/kubernetes-csi/external-provisioner"},
	"canonical/metallb-controller":        {Name: "metallb-controller", RepoURL: "https://github.com/metallb/metallb"},
	"canonical/metallb-speaker":           {Name: "metallb-speaker", RepoURL: "https://github.com/metallb/metallb"},
	"canonical/frr":                       {Name: "frr", RepoURL: "https://github.com/FRRouting/frr"},
	"canonical/k8s-snap/pause":            {Name: "pause", RepoURL: ""},
}

// CurrentComponents returns components from the installed snap by reading $SNAP/images.txt.
func CurrentComponents() []ComponentInfo {
	snapDir := os.Getenv("SNAP")
	if snapDir == "" {
		return nil
	}
	imagesPath := filepath.Join(snapDir, "images.txt")
	return readComponentsFromFile(imagesPath)
}

// ParseImageLines parses raw image strings (one per line) into ComponentInfo list.
func ParseImageLines(lines []string) []ComponentInfo {
	return parseImageList(lines)
}

func readComponentsFromFile(path string) []ComponentInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return parseImageList(lines)
}

// parseImageList converts image strings into ComponentInfo entries.
func parseImageList(lines []string) []ComponentInfo {
	var result []ComponentInfo
	seen := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		repoPath := parts[0]
		tag := parts[1]

		for key, mapping := range componentMappings {
			if strings.Contains(repoPath, key) {
				if mapping.Name == "kubernetes" && seen[mapping.Name] {
					continue
				}
				seen[mapping.Name] = true
				result = append(result, ComponentInfo{
					Name:    mapping.Name,
					Version: tag,
					RepoURL: mapping.RepoURL,
				})
				break
			}
		}
	}
	return result
}
