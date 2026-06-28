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

func TestListEvents(t *testing.T) {
	testCases := []struct {
		name            string
		namespace       string
		listOptions     metav1.ListOptions
		eventList       *corev1.EventList
		listError       error
		expectedReasons []string
		expectedError   string
	}{
		{
			name:            "Empty namespace returns empty slice",
			namespace:       "test-namespace",
			eventList:       &corev1.EventList{},
			expectedReasons: []string{},
		},
		{
			name:      "Returns all events",
			namespace: "kube-system",
			eventList: &corev1.EventList{
				Items: []corev1.Event{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "evt1", Namespace: "kube-system"},
						Reason:     "FailedScheduling",
						Type:       corev1.EventTypeWarning,
						InvolvedObject: corev1.ObjectReference{
							Kind: "Pod",
							Name: "cilium-xyz",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "evt2", Namespace: "kube-system"},
						Reason:     "Pulled",
						Type:       corev1.EventTypeNormal,
					},
				},
			},
			expectedReasons: []string{"FailedScheduling", "Pulled"},
		},
		{
			name:          "Propagates list error",
			namespace:     "test-namespace",
			listError:     fmt.Errorf("boom"),
			expectedError: "failed to list events: boom",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			clientset := fake.NewSimpleClientset()
			client := &Client{Interface: clientset}

			clientset.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if tc.listError != nil {
					return true, nil, tc.listError
				}
				return true, tc.eventList, nil
			})

			events, err := client.ListEvents(context.Background(), tc.namespace, tc.listOptions)

			if tc.expectedError != "" {
				g.Expect(err).Should(MatchError(tc.expectedError))
				g.Expect(events).Should(BeNil())
				return
			}

			g.Expect(err).ShouldNot(HaveOccurred())
			got := make([]string, 0, len(events))
			for _, e := range events {
				got = append(got, e.Reason)
			}
			g.Expect(got).Should(ConsistOf(tc.expectedReasons))
		})
	}
}
