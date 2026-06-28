package kubernetes

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestListPods(t *testing.T) {
	testCases := []struct {
		name          string
		namespace     string
		listOptions   metav1.ListOptions
		podList       *corev1.PodList
		listError     error
		expectedNames []string
		expectedError string
	}{
		{
			name:          "Empty namespace returns empty slice",
			namespace:     "test-namespace",
			podList:       &corev1.PodList{},
			expectedNames: []string{},
		},
		{
			name:      "Returns all pods",
			namespace: "kube-system",
			podList: &corev1.PodList{
				Items: []corev1.Pod{
					{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "kube-system"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "kube-system"}},
				},
			},
			expectedNames: []string{"pod1", "pod2"},
		},
		{
			name:          "Propagates list error",
			namespace:     "test-namespace",
			listError:     fmt.Errorf("boom"),
			expectedError: "failed to list pods: boom",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			clientset := fake.NewSimpleClientset()
			client := &Client{Interface: clientset}

			clientset.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if tc.listError != nil {
					return true, nil, tc.listError
				}
				return true, tc.podList, nil
			})

			pods, err := client.ListPods(context.Background(), tc.namespace, tc.listOptions)

			if tc.expectedError != "" {
				g.Expect(err).Should(MatchError(tc.expectedError))
				g.Expect(pods).Should(BeNil())
				return
			}

			g.Expect(err).ShouldNot(HaveOccurred())
			got := make([]string, 0, len(pods))
			for _, p := range pods {
				got = append(got, p.Name)
			}
			g.Expect(got).Should(ConsistOf(tc.expectedNames))
		})
	}
}

func TestCheckForReadyPods(t *testing.T) {
	testCases := []struct {
		name          string
		namespace     string
		listOptions   metav1.ListOptions
		podList       *corev1.PodList
		listError     error
		expectedError string
	}{
		{
			name:          "No pods",
			namespace:     "test-namespace",
			podList:       &corev1.PodList{},
			expectedError: "no pods in test-namespace namespace on the cluster",
		},
		{
			name:      "All pods ready",
			namespace: "test-namespace",
			podList: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							Conditions: []corev1.PodCondition{
								{Type: corev1.PodReady, Status: corev1.ConditionTrue},
							},
						},
					},
				},
			},
			expectedError: "",
		},
		{
			name:      "Some pods not ready",
			namespace: "test-namespace",
			podList: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							Conditions: []corev1.PodCondition{
								{Type: corev1.PodReady, Status: corev1.ConditionTrue},
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "pod2"},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending,
						},
					},
				},
			},
			expectedError: "pods [pod2] not ready",
		},
		{
			name:          "Error listing pods",
			namespace:     "test-namespace",
			listError:     fmt.Errorf("list error"),
			expectedError: "failed to list pods: list error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			clientset := fake.NewSimpleClientset()
			client := &Client{
				Interface: clientset,
			}

			// Setup fake client responses
			clientset.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if tc.listError != nil {
					return true, nil, tc.listError
				}
				return true, tc.podList, nil
			})

			err := client.CheckForReadyPods(context.Background(), tc.namespace, tc.listOptions)

			if tc.expectedError == "" {
				g.Expect(err).ShouldNot(HaveOccurred())
			} else {
				g.Expect(err).Should(MatchError(tc.expectedError))
			}
		})
	}
}
