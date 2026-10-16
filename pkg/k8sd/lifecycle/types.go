package lifecycle

// Phase is nodeStatus[n]: the durable journal state of a node.
type Phase string

const (
	PhaseAbsent      Phase = "Absent"
	PhaseJoining     Phase = "Joining"
	PhaseActive      Phase = "Active"
	PhaseRollingBack Phase = "RollingBack"
	PhaseRemoving    Phase = "Removing"
)

// Transitional reports whether the phase must be owned by the lease holder.
func (p Phase) Transitional() bool { return p == PhaseJoining || p.Teardown() }

// Teardown reports whether the node is being rolled back or removed.
func (p Phase) Teardown() bool { return p == PhaseRollingBack || p == PhaseRemoving }

// Valid reports whether p is a known phase.
func (p Phase) Valid() bool {
	switch p {
	case PhaseAbsent, PhaseJoining, PhaseActive, PhaseRollingBack, PhaseRemoving:
		return true
	}
	return false
}

// MemberRole is mcRole[n]: microcluster (dqlite) membership.
type MemberRole string

const (
	MemberNone    MemberRole = "none"
	MemberPending MemberRole = "pending"
	MemberJoined  MemberRole = "member"
)

// EtcdRole is etcdRole[n]: etcd raft membership.
type EtcdRole string

const (
	EtcdNone    EtcdRole = "none"
	EtcdLearner EtcdRole = "learner"
	EtcdVoter   EtcdRole = "voter"
)

// Step is one orchestrator action of NodeLifecycle.tla.
type Step string

const (
	StepNone           Step = ""
	StepAddLearner     Step = "AddLearner"
	StepStartEtcd      Step = "StartEtcd"
	StepPromote        Step = "Promote"
	StepActivate       Step = "Activate"
	StepRemoveVoter    Step = "RemoveVoter"
	StepStopEtcd       Step = "StopEtcd"
	StepRemoveLearner  Step = "RemoveLearner"
	StepLeaveCluster   Step = "LeaveCluster"
	StepFinishTeardown Step = "FinishTeardown"
)

// EtcdMember is an etcd cluster member as reported by MemberList.
// Name is empty for a learner whose local etcd has never started.
type EtcdMember struct {
	ID        uint64
	Name      string
	PeerURLs  []string
	IsLearner bool
}

// HostStatus is the node-local runtime state. An unreachable host is
// reported as Up=false; partitions and crashes are indistinguishable.
type HostStatus struct {
	Up          bool
	EtcdRunning bool
}

// Record is one journal row.
type Record struct {
	Name    string
	PeerURL string
	Phase   Phase
}

// JournalState is the replicated orchestrator state: lease plus all rows.
// Nodes without a row are Absent. Lease is "" when free.
type JournalState struct {
	Lease   string
	Records map[string]Record
}

// Transition is a guarded journal update. It applies iff the current lease
// equals FromLease and the node's phase equals FromPhase.
type Transition struct {
	Node      string
	PeerURL   string
	FromPhase Phase
	ToPhase   Phase
	FromLease string
	ToLease   string
}
