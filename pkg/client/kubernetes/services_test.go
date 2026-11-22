package kubernetes

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetServiceClusterIP(t *testing.T) {
	tests := []struct {
		name           string
		serviceObjects []runtime.Object
		serviceName    string
		namespace      string
		expectedIP     string
		expectError    bool
	}{
		{
			name: "service exists",
			serviceObjects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service",
						Namespace: "default",
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "192.168.1.1",
					},
				},
			},
			serviceName: "test-service",
			namespace:   "default",
			expectedIP:  "192.168.1.1",
			expectError: false,
		},
		{
			name:           "service does not exist",
			serviceObjects: []runtime.Object{},
			serviceName:    "nonexistent-service",
			namespace:      "default",
			expectedIP:     "",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			clientset := fake.NewSimpleClientset(tt.serviceObjects...)
			client := &Client{Interface: clientset}

			ip, err := client.GetServiceClusterIP(context.Background(), tt.serviceName, tt.namespace)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(ip).To(BeEmpty())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ip).To(Equal(tt.expectedIP))
			}
		})
	}
}

func TestFindServiceByClusterIP(t *testing.T) {
	tests := []struct {
		name           string
		serviceObjects []runtime.Object
		searchIP       string
		expectedFound  bool
		expectedName   string
	}{
		{
			name: "exact cluster IP match",
			serviceObjects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-1", Namespace: "default"},
					Spec:       corev1.ServiceSpec{ClusterIP: "10.152.183.10"},
				},
			},
			searchIP:      "10.152.183.10",
			expectedFound: true,
			expectedName:  "svc-1",
		},
		{
			name: "equivalent IP representation match",
			serviceObjects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-2", Namespace: "kube-system"},
					Spec:       corev1.ServiceSpec{ClusterIP: "10.152.183.10"},
				},
			},
			searchIP:      "::ffff:10.152.183.10",
			expectedFound: true,
			expectedName:  "svc-2",
		},
		{
			name: "cluster IPs list match",
			serviceObjects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-3", Namespace: "default"},
					Spec:       corev1.ServiceSpec{ClusterIP: "fd00::1", ClusterIPs: []string{"fd00::1", "10.152.183.20"}},
				},
			},
			searchIP:      "10.152.183.20",
			expectedFound: true,
			expectedName:  "svc-3",
		},
		{
			name: "not found",
			serviceObjects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-4", Namespace: "default"},
					Spec:       corev1.ServiceSpec{ClusterIP: "10.152.183.30"},
				},
			},
			searchIP:      "10.152.183.40",
			expectedFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			client := &Client{Interface: fake.NewSimpleClientset(tt.serviceObjects...)}
			svc, found, err := client.FindServiceByClusterIP(context.Background(), tt.searchIP)

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(Equal(tt.expectedFound))
			if tt.expectedFound {
				g.Expect(svc.Name).To(Equal(tt.expectedName))
			}
		})
	}
}
