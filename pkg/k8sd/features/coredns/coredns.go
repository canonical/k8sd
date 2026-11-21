package coredns

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/canonical/k8sd/pkg/client/helm"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/utils"
	"helm.sh/helm/v4/pkg/storage/driver"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

const (
	enabledMsgTmpl      = "enabled at %s"
	disabledMsg         = "disabled"
	deleteFailedMsgTmpl = "Failed to delete DNS, the error was: %v"
	deployFailedMsgTmpl = "Failed to deploy DNS, the error was: %v"

	// DeploymentName is the name of the CoreDNS deployment.
	DeploymentName = "coredns"

	recoveryLeaseName = "ck-dns-recovery"
)

// RecoveryLeaseDuration and RecoveryLeaseRenewInterval configure the CoreDNS recovery Lease TTL and renewal cadence.
var (
	RecoveryLeaseDuration      = 60 * time.Second
	RecoveryLeaseRenewInterval = 20 * time.Second
)

type leaseObservation struct {
	uid           k8stypes.UID
	specBytes     []byte
	firstObserved time.Time
}

var (
	observedLeasesMu sync.Mutex
	observedLeases   = map[string]leaseObservation{}
)

// ResetObservedRecoveryLeases clears cached local Lease observations (used for test isolation).
func ResetObservedRecoveryLeases() {
	observedLeasesMu.Lock()
	defer observedLeasesMu.Unlock()
	clear(observedLeases)
}

// Tolerations allow CoreDNS to be scheduled on control-plane nodes.
// Callers must not modify this shared scheduling configuration.
var Tolerations = []corev1.Toleration{
	{
		Key:      "node-role.kubernetes.io/control-plane",
		Operator: corev1.TolerationOpExists,
		Effect:   corev1.TaintEffectNoSchedule,
	},
}

// ApplyDNS manages the deployment of CoreDNS, with customization options from dns and kubelet, which are retrieved from the cluster configuration.
// ApplyDNS will uninstall CoreDNS from the cluster if dns.Enabled is false.
// ApplyDNS will install or refresh CoreDNS if dns.Enabled is true.
// ApplyDNS will return the ClusterIP address of the coredns service, if successful.
// ApplyDNS will always return a FeatureStatus indicating the current status of the
// deployment.
// ApplyDNS returns an error if anything fails. The error is also wrapped in the .Message field of the
// returned FeatureStatus.
func ApplyDNS(ctx context.Context, snap snap.Snap, dns types.DNS, kubelet types.Kubelet, _ types.Annotations) (types.FeatureStatus, string, error) {
	m := snap.HelmClient()

	if !dns.GetEnabled() {
		if _, err := m.Apply(ctx, Chart, helm.StateDeleted, nil); err != nil {
			err = fmt.Errorf("failed to uninstall coredns: %w", err)
			return types.FeatureStatus{
				Enabled: false,
				Version: ImageTag,
				Message: fmt.Sprintf(deleteFailedMsgTmpl, err),
			}, "", err
		}
		return types.FeatureStatus{
			Enabled: false,
			Version: ImageTag,
			Message: disabledMsg,
		}, "", nil
	}
	clusterIP := kubelet.GetClusterDNS()
	serviceValues := map[string]any{
		"name":      "coredns",
		"clusterIP": clusterIP,
	}
	opCtx := ctx
	client, err := snap.KubernetesClient("")
	if err != nil {
		err = fmt.Errorf("failed to create kubernetes client: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: ImageTag,
			Message: fmt.Sprintf(deployFailedMsgTmpl, err),
		}, "", err
	}

	oldConfig, deployed, err := m.GetDeployedConfig(opCtx, Chart)
	if err != nil {
		err = fmt.Errorf("failed to get deployed release config: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: ImageTag,
			Message: fmt.Sprintf(deployFailedMsgTmpl, err),
		}, "", err
	}

	if client != nil {
		needsLease := !deployed
		if deployed || clusterIP != "" {
			liveIP, err := client.GetServiceClusterIP(opCtx, "coredns", Chart.Namespace)
			if err != nil && !apierrors.IsNotFound(err) {
				err = fmt.Errorf("failed to retrieve the coredns service: %w", err)
				return types.FeatureStatus{
					Enabled: false,
					Version: ImageTag,
					Message: fmt.Sprintf(deployFailedMsgTmpl, err),
				}, "", err
			}
			serviceNotFound := apierrors.IsNotFound(err)
			if deployed {
				if serviceNotFound || (clusterIP != "" && !utils.EqualIP(liveIP, clusterIP)) {
					needsLease = true
				} else {
					preserveDynamicClusterIP(oldConfig, serviceValues)
				}
			}
		}

		if needsLease {
			var releaseLease func()
			opCtx, releaseLease, err = acquireRecoveryLease(ctx, client, Chart.Namespace)
			if err != nil {
				err = fmt.Errorf("failed to acquire coredns recovery lease: %w", err)
				return types.FeatureStatus{
					Enabled: false,
					Version: ImageTag,
					Message: fmt.Sprintf(deployFailedMsgTmpl, err),
				}, "", err
			}
			defer releaseLease()

			// Re-read deployed release and live Service under the cluster-wide lease in case
			// another control-plane node already completed installation or recovery.
			oldConfig, deployed, err = m.GetDeployedConfig(opCtx, Chart)
			if err != nil {
				err = fmt.Errorf("failed to re-read deployed release config: %w", err)
				return types.FeatureStatus{
					Enabled: false,
					Version: ImageTag,
					Message: fmt.Sprintf(deployFailedMsgTmpl, err),
				}, "", err
			}
			if deployed {
				liveIP, err := client.GetServiceClusterIP(opCtx, "coredns", Chart.Namespace)
				if err != nil && !apierrors.IsNotFound(err) {
					err = fmt.Errorf("failed to re-check the coredns service: %w", err)
					return types.FeatureStatus{
						Enabled: false,
						Version: ImageTag,
						Message: fmt.Sprintf(deployFailedMsgTmpl, err),
					}, "", err
				}
				serviceNotFound := apierrors.IsNotFound(err)
				if serviceNotFound || (clusterIP != "" && !utils.EqualIP(liveIP, clusterIP)) {
					if err := opCtx.Err(); err != nil {
						err = fmt.Errorf("coredns recovery lease lost before uninstall: %w", err)
						return types.FeatureStatus{
							Enabled: false,
							Version: ImageTag,
							Message: fmt.Sprintf(deployFailedMsgTmpl, err),
						}, "", err
					}
					if _, err := m.Apply(opCtx, Chart, helm.StateDeleted, nil); err != nil {
						err = fmt.Errorf("failed to uninstall coredns to recreate service: %w", err)
						return types.FeatureStatus{
							Enabled: false,
							Version: ImageTag,
							Message: fmt.Sprintf(deployFailedMsgTmpl, err),
						}, "", err
					}
				} else {
					preserveDynamicClusterIP(oldConfig, serviceValues)
				}
			} else {
				incompleteUninstall, err := hasUninstallingReleaseSecret(opCtx, client, Chart.Namespace, Chart.Name)
				if err != nil {
					err = fmt.Errorf("failed to check coredns release secrets: %w", err)
					return types.FeatureStatus{
						Enabled: false,
						Version: ImageTag,
						Message: fmt.Sprintf(deployFailedMsgTmpl, err),
					}, "", err
				}
				if incompleteUninstall {
					if err := opCtx.Err(); err != nil {
						err = fmt.Errorf("coredns recovery lease lost before finishing uninstall: %w", err)
						return types.FeatureStatus{
							Enabled: false,
							Version: ImageTag,
							Message: fmt.Sprintf(deployFailedMsgTmpl, err),
						}, "", err
					}
					if _, err := m.Apply(opCtx, Chart, helm.StateDeleted, nil); err != nil {
						err = fmt.Errorf("failed to finish incomplete coredns uninstall: %w", err)
						return types.FeatureStatus{
							Enabled: false,
							Version: ImageTag,
							Message: fmt.Sprintf(deployFailedMsgTmpl, err),
						}, "", err
					}
				}
			}
		}
	}

	values := map[string]any{
		"image": map[string]any{
			"repository": imageRepo,
			"tag":        ImageTag,
		},
		"service": serviceValues,
		"serviceAccount": map[string]any{
			"create": true,
			"name":   "coredns",
		},
		"priorityClassName": "system-node-critical",
		"tolerations":       Tolerations,
		"deployment": map[string]any{
			"name": DeploymentName,
		},
		"rollingUpdate": map[string]any{
			"maxSurge":       1,
			"maxUnavailable": 0,
		},
		"servers": []map[string]any{
			{
				"zones": []map[string]any{
					{
						"zone":    ".",
						"use_tcp": true,
					},
				},
				"port": 53,
				"plugins": []map[string]any{
					{"name": "errors"},
					{"name": "health", "configBlock": "lameduck 5s"},
					{"name": "ready"},
					{
						"name":        "kubernetes",
						"parameters":  fmt.Sprintf("%s in-addr.arpa ip6.arpa", kubelet.GetClusterDomain()),
						"configBlock": "pods insecure\nfallthrough in-addr.arpa ip6.arpa\nttl 30",
					},
					{"name": "prometheus", "parameters": "0.0.0.0:9153"},
					{"name": "forward", "parameters": fmt.Sprintf(". %s", strings.Join(dns.GetUpstreamNameservers(), " "))},
					{"name": "cache", "parameters": "30"},
					{"name": "loop"},
					{"name": "reload"},
					{"name": "loadbalance"},
				},
			},
		},
		// PodAntiAffinity: Preferably schedule CoreDNS pods on separate nodes.
		"affinity": map[string]any{
			"podAntiAffinity": map[string]any{
				"preferredDuringSchedulingIgnoredDuringExecution": []map[string]any{
					{
						"weight": 100,
						"podAffinityTerm": map[string]any{
							"labelSelector": map[string]any{
								"matchLabels": map[string]any{
									"app.kubernetes.io/name":     "coredns",
									"app.kubernetes.io/instance": Chart.Name,
									"k8s-app":                    "coredns",
								},
							},
							"topologyKey":    "kubernetes.io/hostname",
							"matchLabelKeys": []string{"pod-template-hash"},
						},
					},
				},
			},
		},
		// TopologySpreadConstraints: Evenly distribute CoreDNS pods across zones and nodes.
		"topologySpreadConstraints": []map[string]any{
			{
				"maxSkew":           1,
				"topologyKey":       "topology.kubernetes.io/zone",
				"whenUnsatisfiable": "ScheduleAnyway",
				"labelSelector": map[string]any{
					"matchLabels": map[string]any{
						"app.kubernetes.io/name":     "coredns",
						"app.kubernetes.io/instance": Chart.Name,
						"k8s-app":                    "coredns",
					},
				},
				"matchLabelKeys": []string{"pod-template-hash"},
			},
			{
				"maxSkew":           1,
				"topologyKey":       "kubernetes.io/hostname",
				"whenUnsatisfiable": "ScheduleAnyway",
				"labelSelector": map[string]any{
					"matchLabels": map[string]any{
						"app.kubernetes.io/name":     "coredns",
						"app.kubernetes.io/instance": Chart.Name,
						"k8s-app":                    "coredns",
					},
				},
				"matchLabelKeys": []string{"pod-template-hash"},
			},
		},
		// PDB: Ensure availability of CoreDNS during maintenance.
		"podDisruptionBudget": map[string]any{
			"minAvailable": 1,
		},
		// HPA: Scale pods based on CPU+memory utilization.
		"hpa": map[string]any{
			"enabled":     true,
			"minReplicas": 2,
			"maxReplicas": 100,
			"metrics": []map[string]any{
				{
					"type": "Resource",
					"resource": map[string]any{
						"name": "cpu",
						"target": map[string]any{
							"type":               "Utilization",
							"averageUtilization": 80,
						},
					},
				},
				{
					"type": "Resource",
					"resource": map[string]any{
						"name": "memory",
						"target": map[string]any{
							"type":               "Utilization",
							"averageUtilization": 70,
						},
					},
				},
			},
		},
		// TODO(berkayoz): Adjust the rock to support a stricter security context
		// Below is the workaround to revert https://github.com/coredns/helm/pull/184/
		"securityContext": map[string]any{
			"allowPrivilegeEscalation": true,
			"readOnlyRootFilesystem":   false,
			"capabilities": map[string]any{
				"drop": []string{},
			},
		},
	}

	if err := opCtx.Err(); err != nil {
		err = fmt.Errorf("coredns recovery lease lost before apply: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: ImageTag,
			Message: fmt.Sprintf(deployFailedMsgTmpl, err),
		}, "", err
	}

	if _, err := m.Apply(opCtx, Chart, helm.StatePresent, values); err != nil {
		if !deployed && client != nil && errors.Is(err, driver.ErrNoDeployedReleases) {
			if ctxErr := opCtx.Err(); ctxErr != nil {
				err = fmt.Errorf("coredns recovery lease lost before finishing uninstall: %w", ctxErr)
				return types.FeatureStatus{
					Enabled: false,
					Version: ImageTag,
					Message: fmt.Sprintf(deployFailedMsgTmpl, err),
				}, "", err
			}
			if _, delErr := m.Apply(opCtx, Chart, helm.StateDeleted, nil); delErr != nil {
				err = fmt.Errorf("failed to finish incomplete coredns uninstall: %w", delErr)
				return types.FeatureStatus{
					Enabled: false,
					Version: ImageTag,
					Message: fmt.Sprintf(deployFailedMsgTmpl, err),
				}, "", err
			}
			if ctxErr := opCtx.Err(); ctxErr != nil {
				err = fmt.Errorf("coredns recovery lease lost before apply: %w", ctxErr)
				return types.FeatureStatus{
					Enabled: false,
					Version: ImageTag,
					Message: fmt.Sprintf(deployFailedMsgTmpl, err),
				}, "", err
			}
			_, err = m.Apply(opCtx, Chart, helm.StatePresent, values)
		}
		if err != nil {
			err = fmt.Errorf("failed to apply coredns: %w", err)
			return types.FeatureStatus{
				Enabled: false,
				Version: ImageTag,
				Message: fmt.Sprintf(deployFailedMsgTmpl, err),
			}, "", err
		}
	}

	if client == nil {
		err = fmt.Errorf("failed to create kubernetes client: client is nil")
		return types.FeatureStatus{
			Enabled: false,
			Version: ImageTag,
			Message: fmt.Sprintf(deployFailedMsgTmpl, err),
		}, "", err
	}
	dnsIP, err := client.GetServiceClusterIP(opCtx, "coredns", Chart.Namespace)
	if err != nil {
		err = fmt.Errorf("failed to retrieve the coredns service: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: ImageTag,
			Message: fmt.Sprintf(deployFailedMsgTmpl, err),
		}, "", err
	}

	if err := opCtx.Err(); err != nil {
		err = fmt.Errorf("coredns recovery lease lost during apply: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: ImageTag,
			Message: fmt.Sprintf(deployFailedMsgTmpl, err),
		}, "", err
	}
	return types.FeatureStatus{
		Enabled: true,
		Version: ImageTag,
		Message: fmt.Sprintf(enabledMsgTmpl, dnsIP),
	}, dnsIP, err
}

// preserveDynamicClusterIP keeps the deployed release's empty, nil, or omitted service.clusterIP
// representation when the live coredns Service already matches clusterIP, avoiding redundant upgrades.
func preserveDynamicClusterIP(oldConfig map[string]any, serviceValues map[string]any) {
	svcMap, ok := oldConfig["service"].(map[string]any)
	if !ok {
		return
	}
	rawIP, exists := svcMap["clusterIP"]
	if !exists {
		delete(serviceValues, "clusterIP")
	} else if rawIP == nil {
		serviceValues["clusterIP"] = nil
	} else if oldIP, ok := rawIP.(string); ok && oldIP == "" {
		serviceValues["clusterIP"] = ""
	}
}

// acquireRecoveryLease acquires and periodically renews a cluster-wide Kubernetes Lease to
// serialize CoreDNS installation and out-of-band Service recovery across control-plane nodes.
// If lease renewal fails or ownership is lost, the returned context is cancelled immediately.
func acquireRecoveryLease(ctx context.Context, client *kubernetes.Client, namespace string) (context.Context, func(), error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, nil, fmt.Errorf("failed to generate lease holder identity: %w", err)
	}
	holderID := hex.EncodeToString(token[:])
	leases := client.CoordinationV1().Leases(namespace)

	leaseDuration := RecoveryLeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 60 * time.Second
	}
	interval := RecoveryLeaseRenewInterval
	if interval <= 0 || interval >= leaseDuration {
		interval = leaseDuration / 3
	}
	safetyMargin := max(interval/2, leaseDuration/6)
	if safetyMargin >= leaseDuration {
		safetyMargin = leaseDuration / 2
	}
	acquireTimeout := leaseDuration - safetyMargin
	leaseDurationSeconds := max(int32(leaseDuration/time.Second), 1)
	obsKey := namespace + "/" + recoveryLeaseName

	var acquired *coordinationv1.Lease
	var acquiredAt time.Time
	for range 2 {
		now := time.Now()
		microNow := metav1.NewMicroTime(now)
		candidate := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      recoveryLeaseName,
				Namespace: namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &holderID,
				LeaseDurationSeconds: &leaseDurationSeconds,
				AcquireTime:          &microNow,
				RenewTime:            &microNow,
			},
		}

		reqCtx, cancelReq := context.WithTimeout(ctx, acquireTimeout)
		created, err := leases.Create(reqCtx, candidate, metav1.CreateOptions{})
		cancelReq()
		if err == nil {
			acquired = created
			acquiredAt = now
			clearObservedRecoveryLease(obsKey)
			break
		}
		if !apierrors.IsAlreadyExists(err) {
			return nil, nil, err
		}

		getCtx, cancelGet := context.WithTimeout(ctx, acquireTimeout)
		existing, getErr := leases.Get(getCtx, recoveryLeaseName, metav1.GetOptions{})
		cancelGet()
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				clearObservedRecoveryLease(obsKey)
				continue
			}
			return nil, nil, getErr
		}

		dur := leaseDuration
		if leaseDuration >= time.Second && existing.Spec.LeaseDurationSeconds != nil && *existing.Spec.LeaseDurationSeconds > 0 {
			dur = time.Duration(*existing.Spec.LeaseDurationSeconds) * time.Second
		}
		if !isPeerLeaseExpiredLocally(obsKey, existing, dur) {
			return nil, nil, fmt.Errorf("coredns service recovery is already in progress (holder %q)", ptr.Deref(existing.Spec.HolderIdentity, ""))
		}

		updNow := time.Now()
		microUpdNow := metav1.NewMicroTime(updNow)
		updatedLease := existing.DeepCopy()
		updatedLease.Spec.HolderIdentity = &holderID
		updatedLease.Spec.LeaseDurationSeconds = &leaseDurationSeconds
		updatedLease.Spec.AcquireTime = &microUpdNow
		updatedLease.Spec.RenewTime = &microUpdNow

		updCtx, cancelUpd := context.WithTimeout(ctx, acquireTimeout)
		updated, updErr := leases.Update(updCtx, updatedLease, metav1.UpdateOptions{})
		cancelUpd()
		if updErr != nil {
			return nil, nil, updErr
		}
		acquired = updated
		acquiredAt = updNow
		clearObservedRecoveryLease(obsKey)
		break
	}
	if acquired == nil {
		return nil, nil, fmt.Errorf("coredns service recovery lease contended")
	}

	lastRenew := acquiredAt
	if time.Since(lastRenew) >= leaseDuration-safetyMargin {
		deleteRecoveryLease(ctx, leases, acquired)
		return nil, nil, fmt.Errorf("coredns recovery lease exceeded renewal safety margin during acquisition")
	}

	current := acquired.DeepCopy()
	if time.Since(lastRenew) >= interval {
		remaining := (leaseDuration - safetyMargin) - time.Since(lastRenew)
		if remaining <= 0 {
			deleteRecoveryLease(ctx, leases, current)
			return nil, nil, fmt.Errorf("coredns recovery lease expired before initial renewal")
		}
		renewCtx, cancelRenew := context.WithTimeout(ctx, remaining)
		renewNow := time.Now()
		microRenewNow := metav1.NewMicroTime(renewNow)
		next := current.DeepCopy()
		next.Spec.RenewTime = &microRenewNow
		updated, err := leases.Update(renewCtx, next, metav1.UpdateOptions{})
		cancelRenew()
		if err != nil || ptr.Deref(updated.Spec.HolderIdentity, "") != holderID || time.Since(renewNow) >= leaseDuration-safetyMargin {
			deleteRecoveryLease(ctx, leases, current)
			return nil, nil, fmt.Errorf("failed immediate renewal after slow lease acquisition: %w", err)
		}
		lastRenew = renewNow
		current = updated
	}

	leaseCtx, cancelLease := context.WithCancel(ctx)
	var lostOwnership atomic.Bool
	renewDone := make(chan struct{})

	go func() {
		defer close(renewDone)
		timer := time.NewTimer(max(interval-time.Since(lastRenew), 0))
		defer timer.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-timer.C:
				remaining := (leaseDuration - safetyMargin) - time.Since(lastRenew)
				if remaining <= 0 {
					lostOwnership.Store(true)
					cancelLease()
					return
				}
				renewCtx, cancelRenew := context.WithTimeout(leaseCtx, remaining)
				now := time.Now()
				microNow := metav1.NewMicroTime(now)
				next := current.DeepCopy()
				next.Spec.RenewTime = &microNow
				updated, err := leases.Update(renewCtx, next, metav1.UpdateOptions{})
				cancelRenew()
				if err != nil || ptr.Deref(updated.Spec.HolderIdentity, "") != holderID || time.Since(now) >= leaseDuration-safetyMargin {
					lostOwnership.Store(true)
					cancelLease()
					return
				}
				lastRenew = now
				current = updated
				timer.Reset(max(interval-time.Since(lastRenew), 0))
			}
		}
	}()

	return leaseCtx, func() {
		cancelLease()
		<-renewDone
		clearObservedRecoveryLease(obsKey)
		if lostOwnership.Load() {
			return
		}
		deleteRecoveryLease(ctx, leases, current)
	}, nil
}

// isPeerLeaseExpiredLocally determines whether an existing Lease held by another node has expired
// using local monotonic observations of unchanged LeaseSpec state (immune to cross-node clock skew).
func isPeerLeaseExpiredLocally(key string, existing *coordinationv1.Lease, dur time.Duration) bool {
	if ptr.Deref(existing.Spec.HolderIdentity, "") == "" {
		clearObservedRecoveryLease(key)
		return true
	}
	specBytes, err := json.Marshal(existing.Spec)
	if err != nil {
		return false
	}

	now := time.Now()
	observedLeasesMu.Lock()
	defer observedLeasesMu.Unlock()

	obs, seen := observedLeases[key]
	if !seen || obs.uid != existing.UID || !bytes.Equal(obs.specBytes, specBytes) {
		observedLeases[key] = leaseObservation{
			uid:           existing.UID,
			specBytes:     specBytes,
			firstObserved: now,
		}
		return false
	}
	return now.Sub(obs.firstObserved) > dur
}

func clearObservedRecoveryLease(key string) {
	observedLeasesMu.Lock()
	defer observedLeasesMu.Unlock()
	delete(observedLeases, key)
}

func deleteRecoveryLease(ctx context.Context, leases interface {
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
}, lease *coordinationv1.Lease,
) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	opts := metav1.DeleteOptions{}
	if lease.UID != "" || lease.ResourceVersion != "" {
		preconds := &metav1.Preconditions{}
		if lease.UID != "" {
			uid := lease.UID
			preconds.UID = &uid
		}
		if lease.ResourceVersion != "" {
			rv := lease.ResourceVersion
			preconds.ResourceVersion = &rv
		}
		opts.Preconditions = preconds
	}
	_ = leases.Delete(releaseCtx, recoveryLeaseName, opts)
}

// hasUninstallingReleaseSecret reports whether Kubernetes Secrets storage still holds an incomplete
// uninstalling revision for releaseName (e.g. after a failed recovery uninstall).
func hasUninstallingReleaseSecret(ctx context.Context, client *kubernetes.Client, namespace, releaseName string) (bool, error) {
	secrets, err := client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("owner=helm,name=%s,status=uninstalling", releaseName),
		Limit:         1,
	})
	if err != nil {
		return false, err
	}
	return len(secrets.Items) > 0, nil
}
