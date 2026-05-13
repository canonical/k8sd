package preflight

import (
	"strings"

	"github.com/canonical/k8sd/pkg/k8sd/images"
)

// componentMapping maps image repo sub-paths to component info.
type componentMapping struct {
	Name    string
	RepoURL string
}

var componentMappings = map[string]componentMapping{
	"cilium/cilium":                                        {Name: "cilium", RepoURL: "https://github.com/cilium/cilium"},
	"cilium/operator-generic":                              {Name: "cilium-operator", RepoURL: "https://github.com/cilium/cilium"},
	"k8s-artifacts-prod/binaries/cilium":                   {Name: "cilium", RepoURL: "https://github.com/cilium/cilium"},
	"k8s-artifacts-prod/binaries/cilium-operator-generic":  {Name: "cilium-operator", RepoURL: "https://github.com/cilium/cilium"},
	"registry.k8s.io/coredns":                              {Name: "coredns", RepoURL: "https://github.com/coredns/coredns"},
	"k8s-artifacts-prod/binaries/coredns":                  {Name: "coredns", RepoURL: "https://github.com/coredns/coredns"},
	"registry.k8s.io/metrics-server":                       {Name: "metrics-server", RepoURL: "https://github.com/kubernetes-sigs/metrics-server"},
	"k8s-artifacts-prod/binaries/metrics-server":           {Name: "metrics-server", RepoURL: "https://github.com/kubernetes-sigs/metrics-server"},
	"registry.k8s.io/sig-storage/local-volume-provisioner": {Name: "localpv", RepoURL: "https://github.com/kubernetes-sigs/sig-storage-local-static-provisioner"},
	"k8s-artifacts-prod/binaries/local-volume-provisioner": {Name: "localpv", RepoURL: "https://github.com/kubernetes-sigs/sig-storage-local-static-provisioner"},
	"registry.k8s.io/etcd":                                 {Name: "etcd", RepoURL: "https://github.com/etcd-io/etcd"},
	"k8s-artifacts-prod/binaries/etcd":                     {Name: "etcd", RepoURL: "https://github.com/etcd-io/etcd"},
	"registry.k8s.io/kube-apiserver":                       {Name: "kubernetes", RepoURL: "https://github.com/kubernetes/kubernetes"},
	"registry.k8s.io/kube-controller-manager":              {Name: "kubernetes", RepoURL: "https://github.com/kubernetes/kubernetes"},
	"registry.k8s.io/kube-scheduler":                       {Name: "kubernetes", RepoURL: "https://github.com/kubernetes/kubernetes"},
	"registry.k8s.io/kube-proxy":                           {Name: "kubernetes", RepoURL: "https://github.com/kubernetes/kubernetes"},
	"k8s-artifacts-prod/binaries/kube-apiserver":           {Name: "kubernetes", RepoURL: "https://github.com/kubernetes/kubernetes"},
	"k8s-artifacts-prod/binaries/kube-controller-manager":  {Name: "kubernetes", RepoURL: "https://github.com/kubernetes/kubernetes"},
	"k8s-artifacts-prod/binaries/kube-scheduler":           {Name: "kubernetes", RepoURL: "https://github.com/kubernetes/kubernetes"},
	"k8s-artifacts-prod/binaries/kube-proxy":               {Name: "kubernetes", RepoURL: "https://github.com/kubernetes/kubernetes"},
	"registry.k8s.io/pause":                                {Name: "pause", RepoURL: ""},
	"k8s-artifacts-prod/binaries/pause":                    {Name: "pause", RepoURL: ""},
}

// CurrentComponents returns components from the installed snap by parsing images.Images().
func CurrentComponents() []ComponentInfo {
	var result []ComponentInfo
	seen := make(map[string]bool)

	for _, image := range images.Images() {
		parts := strings.SplitN(image, ":", 2)
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

// ParseImageLines parses raw image strings (one per line) into ComponentInfo list.
func ParseImageLines(lines []string) []ComponentInfo {
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
