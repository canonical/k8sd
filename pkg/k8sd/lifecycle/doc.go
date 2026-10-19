// Package lifecycle implements the control-plane node join/remove lifecycle
// specified and model-checked in spec/NodeLifecycle.tla.
//
// The orchestrator is level-triggered: each Step observes the cluster
// (journal, microcluster, etcd, hosts), plans exactly one action with the
// pure function Plan, and executes it. No in-memory progress is trusted, so a
// crash or a lost RPC response between any two steps is recovered by the
// next observation. TLC verifies that at most one step is enabled per node in
// every reachable state (ProgressDeterministic), which is what makes Plan a
// function rather than a choice.
//
// Spec-to-Go mapping:
//
//	TLA+ variable          Go
//	nodeStatus[n]          NodeView.Phase      (Journal, dqlite)
//	lease                  Snapshot.Lease      (Journal, dqlite; "" = NoNode)
//	mcRole[n]              NodeView.Member     (Membership, microcluster)
//	dqliteRole[n]          NodeView.Dqlite     (Membership, microcluster)
//	etcdRole[n]            NodeView.Etcd       (Datastore, etcd)
//	etcdRunning[n]         NodeView.EtcdRunning(Hosts)
//	up[n]                  NodeView.Up         (Hosts)
//
//	TLA+ action            Go
//	McJoin, ConfirmMember  performed by microcluster before PostJoin
//	McRollback, Rebalance  performed by microcluster / go-dqlite
//	BeginJoin              Orchestrator.BeginJoin (PostJoin hook)
//	AddLearner .. Activate StepAddLearner, StepStartEtcd, StepPromote, StepActivate
//	FailJoin               Orchestrator.Abort
//	BeginRemove            Orchestrator.BeginRemove
//	RemoveVoter .. Finish  StepRemoveVoter, StepStopEtcd, StepRemoveLearner,
//	                       StepLeaveCluster, StepFinishTeardown
//	Crash / Restart        environment (exercised by the test simulator)
//	ForceLeaveCluster      mutation only: remove-node --force before #161
//
// Observe needs etcd and dqlite quorum (MemberList and journal reads are
// linearizable), so where the spec could still take a lease-only step the Go
// code stutters until quorum returns. Stuttering never violates safety.
//
// Cross-process safety relies on two mechanisms only: the journal's
// compare-and-swap (one dqlite transaction) for nodeStatus/lease, and the
// guards etcd and microcluster enforce themselves on membership changes.
// Within one process, Orchestrator serialises steps with a mutex.
package lifecycle
