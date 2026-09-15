package dnsrebalancer

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestNodeEventPredicateReadiness(t *testing.T) {
	states := []struct {
		name   string
		status corev1.ConditionStatus
		ready  bool
	}{
		{"True", corev1.ConditionTrue, true},
		{"False", corev1.ConditionFalse, false},
		{"Unknown", corev1.ConditionUnknown, false},
		{"Missing", "", false},
	}
	newNode := func(status corev1.ConditionStatus) *corev1.Node {
		node := &corev1.Node{}
		if status != "" {
			node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
		}
		return node
	}
	filter := nodeEventPredicate()
	for _, state := range states {
		t.Run("Create/"+state.name, func(t *testing.T) {
			NewWithT(t).Expect(filter.Create(event.CreateEvent{Object: newNode(state.status)})).To(Equal(state.ready))
		})
		for _, next := range states {
			t.Run("Update/"+state.name+"To"+next.name, func(t *testing.T) {
				update := event.UpdateEvent{ObjectOld: newNode(state.status), ObjectNew: newNode(next.status)}
				NewWithT(t).Expect(filter.Update(update)).To(Equal(state.ready != next.ready))
			})
		}
	}
}

func TestNodeEventPredicateUpdates(t *testing.T) {
	baseline := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{
			{Key: "node.cilium.io/agent-not-ready", Effect: corev1.TaintEffectNoSchedule},
			{Key: "node-role.kubernetes.io/control-plane", Effect: corev1.TaintEffectNoSchedule},
		}},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		}},
	}
	cases := []struct {
		name   string
		mutate func(*corev1.Node)
		want   bool
	}{
		{"Unchanged", func(node *corev1.Node) {}, false},
		{"Cordon", func(node *corev1.Node) { node.Spec.Unschedulable = true }, true},
		{"RemoveAllTaints", func(node *corev1.Node) { node.Spec.Taints = nil }, true},
		{"RemoveCNIStartupTaint", func(node *corev1.Node) { node.Spec.Taints = node.Spec.Taints[1:] }, true},
		{"RemoveControlPlaneTaint", func(node *corev1.Node) { node.Spec.Taints = node.Spec.Taints[:1] }, true},
		{"AddNoExecuteTaint", func(node *corev1.Node) {
			node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{Key: "maintenance", Effect: corev1.TaintEffectNoExecute})
		}, true},
		{"TaintKey", func(node *corev1.Node) { node.Spec.Taints[0].Key = "maintenance" }, true},
		{"TaintValue", func(node *corev1.Node) { node.Spec.Taints[0].Value = "pending" }, true},
		{"TaintEffect", func(node *corev1.Node) { node.Spec.Taints[0].Effect = corev1.TaintEffectPreferNoSchedule }, true},
		{"TaintOrder", func(node *corev1.Node) {
			node.Spec.Taints[0], node.Spec.Taints[1] = node.Spec.Taints[1], node.Spec.Taints[0]
		}, true},
		{"TaintTimeAdded", func(node *corev1.Node) {
			timestamp := metav1.NewTime(time.Unix(100, 0))
			node.Spec.Taints[0].TimeAdded = &timestamp
		}, false},
		{"Labels", func(node *corev1.Node) { node.Labels = map[string]string{"example.com/label": "value"} }, false},
		{"Annotations", func(node *corev1.Node) { node.Annotations = map[string]string{"example.com/note": "value"} }, false},
		{"ResourceVersion", func(node *corev1.Node) { node.ResourceVersion = "2" }, false},
		{"Heartbeat", func(node *corev1.Node) {
			node.Status.Conditions[0].LastHeartbeatTime = metav1.NewTime(time.Unix(100, 0))
		}, false},
		{"ReadyConditionDetails", func(node *corev1.Node) {
			node.Status.Conditions[0].Reason = "KubeletReady"
			node.Status.Conditions[0].Message = "ready"
			node.Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Unix(100, 0))
		}, false},
		{"OtherCondition", func(node *corev1.Node) {
			node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue})
		}, false},
		{"Images", func(node *corev1.Node) {
			node.Status.Images = []corev1.ContainerImage{{Names: []string{"example.com/image:latest"}}}
		}, false},
		{"CombinedEligibilityChanges", func(node *corev1.Node) {
			node.Spec.Unschedulable = true
			node.Spec.Taints = nil
			node.Status.Conditions[0].Status = corev1.ConditionFalse
		}, true},
	}
	filter := nodeEventPredicate()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			original := baseline.DeepCopy()
			changed := original.DeepCopy()
			test.mutate(changed)
			for _, direction := range []struct {
				name string
				old  *corev1.Node
				new  *corev1.Node
			}{
				{"Forward", original, changed},
				{"Reverse", changed, original},
			} {
				t.Run(direction.name, func(t *testing.T) {
					NewWithT(t).Expect(filter.Update(event.UpdateEvent{ObjectOld: direction.old, ObjectNew: direction.new})).To(Equal(test.want))
				})
			}
		})
	}
	t.Run("NilAndEmptyTaints", func(t *testing.T) {
		original, changed := baseline.DeepCopy(), baseline.DeepCopy()
		original.Spec.Taints = nil
		changed.Spec.Taints = []corev1.Taint{}
		NewWithT(t).Expect(filter.Update(event.UpdateEvent{ObjectOld: original, ObjectNew: changed})).To(BeFalse())
	})
}

func TestNodeEventPredicateInvalidObjects(t *testing.T) {
	filter := nodeEventPredicate()
	for _, test := range []struct {
		name   string
		object client.Object
	}{
		{"Nil", nil},
		{"NonNode", &corev1.Pod{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(filter.Create(event.CreateEvent{Object: test.object})).To(BeFalse())
			g.Expect(filter.Update(event.UpdateEvent{ObjectOld: test.object, ObjectNew: &corev1.Node{}})).To(BeFalse())
			g.Expect(filter.Update(event.UpdateEvent{ObjectOld: &corev1.Node{}, ObjectNew: test.object})).To(BeFalse())
		})
	}
}

func TestNodeEventPredicateLifecycle(t *testing.T) {
	filter := nodeEventPredicate()
	for _, ready := range []corev1.ConditionStatus{corev1.ConditionTrue, corev1.ConditionFalse, corev1.ConditionUnknown} {
		t.Run(string(ready), func(t *testing.T) {
			g := NewWithT(t)
			node := &corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}}}
			g.Expect(filter.Delete(event.DeleteEvent{Object: node})).To(BeTrue())
			g.Expect(filter.Delete(event.DeleteEvent{Object: node, DeleteStateUnknown: true})).To(BeTrue())
			g.Expect(filter.Generic(event.GenericEvent{Object: node})).To(BeFalse())
		})
	}
}
