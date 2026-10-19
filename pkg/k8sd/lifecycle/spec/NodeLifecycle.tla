--------------------------- MODULE NodeLifecycle ---------------------------
(***************************************************************************)
(* Control-plane node join / remove lifecycle for k8sd (etcd datastore).   *)
(*                                                                         *)
(* Three replicated systems are involved:                                  *)
(*   - microcluster / dqlite : cluster membership + the k8sd journal       *)
(*   - etcd raft             : Kubernetes datastore membership             *)
(*   - the host              : local etcd process, crashes and reboots     *)
(*                                                                         *)
(* The orchestrator is level-triggered: every step observes external state *)
(* and performs at most one atomic external mutation. A crash between any  *)
(* two steps (including "RPC committed, response lost") is therefore just  *)
(* another interleaving that TLC explores.                                 *)
(*                                                                         *)
(* Hook order follows microcluster: `k8s join-cluster` first makes the     *)
(* node a microcluster member (McJoin, ConfirmMember) without any k8sd     *)
(* lease; only then does the PostJoin hook take the lease (BeginJoin). If  *)
(* that fails, microcluster force-deletes the member (McRollback).         *)
(*                                                                         *)
(* dqlite keeps an odd number of voters (at most 3); other members are     *)
(* spares. A 2-node cluster therefore has 1 dqlite voter and 1 spare, while *)
(* etcd has 2 voters. Only dqlite voters count toward dqlite quorum.       *)
(*                                                                         *)
(* StartEtcd and StopEtcd are node-local: they run on the node's own k8sd  *)
(* and need that host to be up. Every other action goes through dqlite or  *)
(* etcd raft and can be driven from any node.                              *)
(*                                                                         *)
(* Network partitions are modelled as crashes: from a quorum point of view *)
(* an unreachable node is indistinguishable from a dead one.               *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    Nodes,        \* candidate control-plane hosts
    Seed,         \* bootstrapped node, initially Active
    NoNode,       \* sentinel: lease not held
    MaxCrashes,   \* bound on crash events so liveness is checkable
    Mutation      \* "none" | "stop-before-remove" | "unguarded-remove" | "force-remove"

ASSUME /\ Seed \in Nodes
       /\ NoNode \notin Nodes
       /\ MaxCrashes \in Nat
       /\ Mutation \in {"none", "stop-before-remove", "unguarded-remove", "force-remove"}

Phases       == {"Absent", "Joining", "Active", "RollingBack", "Removing"}
Transitional == {"Joining", "RollingBack", "Removing"}
Teardown     == {"RollingBack", "Removing"}

VARIABLES
    nodeStatus,   \* [Nodes -> Phases]  durable journal row (dqlite)
    lease,        \* Nodes \cup {NoNode} cluster-wide transition lease (dqlite)
    mcRole,       \* [Nodes -> {"none","pending","member"}] microcluster membership
    dqliteRole,   \* [Nodes -> {"none","spare","voter"}]    dqlite raft role
    etcdRole,     \* [Nodes -> {"none","learner","voter"}]  etcd raft membership
    etcdRunning,  \* [Nodes -> BOOLEAN] local etcd process
    up,           \* [Nodes -> BOOLEAN] host alive and reachable
    crashes       \* number of crash events so far

vars == <<nodeStatus, lease, mcRole, dqliteRole, etcdRole, etcdRunning, up, crashes>>

-----------------------------------------------------------------------------
(* Derived consensus state                                                 *)

Majority(live, all) == 2 * Cardinality(live) > Cardinality(all)

Voters     == {n \in Nodes : etcdRole[n] = "voter"}
Learners   == {n \in Nodes : etcdRole[n] = "learner"}
LiveVoters == {n \in Voters : up[n] /\ etcdRunning[n]}
EtcdQuorum == Majority(LiveVoters, Voters)

EtcdQuorumWithout(n) ==
    LET V == Voters \ {n} IN V # {} /\ Majority(LiveVoters \ {n}, V)

McMembers     == {n \in Nodes : mcRole[n] = "member"}
DqliteVoters  == {n \in Nodes : dqliteRole[n] = "voter"}
DqliteQuorum  == Majority({n \in DqliteVoters : up[n]}, DqliteVoters)

DqliteQuorumWithout(n) ==
    LET V == DqliteVoters \ {n} IN V # {} /\ Majority({m \in V : up[m]}, V)

\* dqlite roles after member n leaves. A voter leaves either because quorum
\* survives without it, or by handing its vote to an online spare first
\* (go-dqlite handover), which is what makes a sole voter removable.
DqliteLeave(n) ==
    \/ /\ dqliteRole[n] # "voter"
       /\ dqliteRole' = [dqliteRole EXCEPT ![n] = "none"]
    \/ /\ dqliteRole[n] = "voter"
       /\ DqliteQuorumWithout(n)
       /\ dqliteRole' = [dqliteRole EXCEPT ![n] = "none"]
    \/ /\ dqliteRole[n] = "voter"
       /\ \E m \in Nodes \ {n} :
            /\ dqliteRole[m] = "spare" /\ up[m]
            /\ dqliteRole' = [dqliteRole EXCEPT ![n] = "none", ![m] = "voter"]

-----------------------------------------------------------------------------
Init ==
    /\ nodeStatus  = [n \in Nodes |-> IF n = Seed THEN "Active" ELSE "Absent"]
    /\ lease       = NoNode
    /\ mcRole      = [n \in Nodes |-> IF n = Seed THEN "member" ELSE "none"]
    /\ dqliteRole  = [n \in Nodes |-> IF n = Seed THEN "voter" ELSE "none"]
    /\ etcdRole    = [n \in Nodes |-> IF n = Seed THEN "voter" ELSE "none"]
    /\ etcdRunning = [n \in Nodes |-> n = Seed]
    /\ up          = [n \in Nodes |-> TRUE]
    /\ crashes     = 0

-----------------------------------------------------------------------------
(* Microcluster membership (before and around the k8sd lifecycle)          *)

\* `k8s join-cluster`: microcluster records the node as PENDING. No lease.
McJoin(n) ==
    /\ nodeStatus[n] = "Absent"
    /\ mcRole[n] = "none"
    /\ etcdRole[n] = "none"
    /\ ~etcdRunning[n]
    /\ up[n]
    /\ DqliteQuorum
    /\ mcRole' = [mcRole EXCEPT ![n] = "pending"]
    /\ UNCHANGED <<nodeStatus, lease, dqliteRole, etcdRole, etcdRunning, up, crashes>>

\* microcluster finishes the dqlite join: PENDING -> member, as a spare.
ConfirmMember(n) ==
    /\ mcRole[n] = "pending"
    /\ up[n]
    /\ DqliteQuorum
    /\ mcRole'     = [mcRole EXCEPT ![n] = "member"]
    /\ dqliteRole' = [dqliteRole EXCEPT ![n] = "spare"]
    /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning, up, crashes>>

\* The join failed before the k8sd lease was taken (or PostJoin could not take
\* it): microcluster force-deletes the member.
McRollback(n) ==
    /\ nodeStatus[n] = "Absent"
    /\ mcRole[n] # "none"
    /\ etcdRole[n] = "none"
    /\ ~etcdRunning[n]
    /\ DqliteQuorum
    /\ DqliteLeave(n)
    /\ mcRole'     = [mcRole EXCEPT ![n] = "none"]
    /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning, up, crashes>>

\* dqlite role rebalancing (go-dqlite app): keep an odd number of voters.
PromoteDqliteVoters(a, b) ==
    /\ a # b
    /\ Cardinality(DqliteVoters) = 1
    /\ dqliteRole[a] = "spare" /\ dqliteRole[b] = "spare"
    /\ up[a] /\ up[b]
    /\ DqliteQuorum
    /\ dqliteRole' = [dqliteRole EXCEPT ![a] = "voter", ![b] = "voter"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole, etcdRunning, up, crashes>>

ReplaceDqliteVoter(old, new) ==
    /\ dqliteRole[old] = "voter" /\ ~up[old]
    /\ dqliteRole[new] = "spare" /\ up[new]
    /\ DqliteQuorum
    /\ dqliteRole' = [dqliteRole EXCEPT ![old] = "spare", ![new] = "voter"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole, etcdRunning, up, crashes>>

DemoteDqliteVoter(v) ==
    /\ Cardinality(DqliteVoters) = 2
    /\ dqliteRole[v] = "voter"
    /\ DqliteQuorum
    /\ dqliteRole' = [dqliteRole EXCEPT ![v] = "spare"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole, etcdRunning, up, crashes>>

Rebalance ==
    \/ \E a, b \in Nodes : PromoteDqliteVoters(a, b)
    \/ \E o, n \in Nodes : ReplaceDqliteVoter(o, n)
    \/ \E v \in Nodes : DemoteDqliteVoter(v)

-----------------------------------------------------------------------------
(* Join path                                                               *)

\* PostJoin hook: one dqlite tx acquires the lease and writes the journal row.
BeginJoin(n) ==
    /\ nodeStatus[n] = "Absent"
    /\ mcRole[n] = "member"
    /\ up[n]
    /\ lease = NoNode
    /\ DqliteQuorum
    /\ lease'      = n
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "Joining"]
    /\ UNCHANGED <<mcRole, dqliteRole, etcdRole, etcdRunning, up, crashes>>

\* MemberAddAsLearner. etcd allows one learner and needs a leader.
AddLearner(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ mcRole[n] = "member"
    /\ etcdRole[n] = "none"
    /\ Learners = {}
    /\ EtcdQuorum
    /\ etcdRole' = [etcdRole EXCEPT ![n] = "learner"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRunning, up, crashes>>

\* Node-local.
StartEtcd(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ etcdRole[n] # "none"
    /\ up[n]
    /\ ~etcdRunning[n]
    /\ etcdRunning' = [etcdRunning EXCEPT ![n] = TRUE]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRole, up, crashes>>

\* MemberPromote. etcd rejects promotion of a learner that is not in sync.
Promote(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ etcdRole[n] = "learner"
    /\ up[n]
    /\ etcdRunning[n]
    /\ EtcdQuorum
    /\ etcdRole' = [etcdRole EXCEPT ![n] = "voter"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRunning, up, crashes>>

\* Services up, upgrade checks passed. Journal commit + lease release are a
\* single dqlite tx.
Activate(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ etcdRole[n] = "voter"
    /\ mcRole[n] = "member"
    /\ up[n]
    /\ etcdRunning[n]
    /\ EtcdQuorum
    /\ DqliteQuorum
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "Active"]
    /\ lease'      = NoNode
    /\ UNCHANGED <<mcRole, dqliteRole, etcdRole, etcdRunning, up, crashes>>

\* Any join step may fail: timeout, promote retries exhausted, version skew.
FailJoin(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ DqliteQuorum
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "RollingBack"]
    /\ UNCHANGED <<lease, mcRole, dqliteRole, etcdRole, etcdRunning, up, crashes>>

-----------------------------------------------------------------------------
(* Remove path                                                             *)

BeginRemove(n) ==
    /\ nodeStatus[n] = "Active"
    /\ lease = NoNode
    /\ DqliteQuorum
    /\ Cardinality(Voters) > 1          \* never remove the last control plane
    /\ lease'      = n
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "Removing"]
    /\ UNCHANGED <<mcRole, dqliteRole, etcdRole, etcdRunning, up, crashes>>

-----------------------------------------------------------------------------
(* Teardown, shared by RollingBack and Removing                            *)

\* A voter is removed FIRST, while its local etcd still counts toward quorum.
RemoveVoter(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ etcdRole[n] = "voter"
    /\ EtcdQuorum
    /\ Mutation = "unguarded-remove" \/ EtcdQuorumWithout(n)
    /\ etcdRole' = [etcdRole EXCEPT ![n] = "none"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRunning, up, crashes>>

\* Node-local.
StopEtcd(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ up[n]
    /\ etcdRunning[n]
    /\ Mutation = "stop-before-remove" \/ etcdRole[n] # "voter"
    /\ etcdRunning' = [etcdRunning EXCEPT ![n] = FALSE]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRole, up, crashes>>

\* Learners never count toward quorum, so removal is always safe.
RemoveLearner(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ etcdRole[n] = "learner"
    /\ ~etcdRunning[n]
    /\ EtcdQuorum
    /\ etcdRole' = [etcdRole EXCEPT ![n] = "none"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRunning, up, crashes>>

\* microcluster RemoveClusterMember.
LeaveCluster(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ etcdRole[n] = "none"
    /\ ~etcdRunning[n]
    /\ mcRole[n] # "none"
    /\ DqliteQuorum
    /\ DqliteLeave(n)
    /\ mcRole'     = [mcRole EXCEPT ![n] = "none"]
    /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning, up, crashes>>

\* Mutation "force-remove" models `k8s remove-node --force` before the fix
\* for #161: when the etcd removal fails, it logs the error and removes the
\* node from microcluster anyway.
ForceLeaveCluster(n) ==
    /\ Mutation = "force-remove"
    /\ nodeStatus[n] = "Removing"
    /\ etcdRole[n] = "voter"
    /\ ~(EtcdQuorum /\ EtcdQuorumWithout(n))
    /\ mcRole[n] # "none"
    /\ DqliteQuorum
    /\ DqliteLeave(n)
    /\ mcRole'     = [mcRole EXCEPT ![n] = "none"]
    /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning, up, crashes>>

FinishTeardown(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ mcRole[n] = "none"
    /\ etcdRole[n] = "none"
    /\ ~etcdRunning[n]
    /\ DqliteQuorum
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "Absent"]
    /\ lease'      = NoNode
    /\ UNCHANGED <<mcRole, dqliteRole, etcdRole, etcdRunning, up, crashes>>

-----------------------------------------------------------------------------
(* Environment                                                             *)

Crash(n) ==
    /\ up[n]
    /\ crashes < MaxCrashes
    /\ up'          = [up EXCEPT ![n] = FALSE]
    /\ etcdRunning' = [etcdRunning EXCEPT ![n] = FALSE]
    /\ crashes'     = crashes + 1
    /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRole>>

\* The etcd snap service stays enabled while the host is an etcd member.
Restart(n) ==
    /\ ~up[n]
    /\ up'          = [up EXCEPT ![n] = TRUE]
    /\ etcdRunning' = [etcdRunning EXCEPT ![n] = etcdRole[n] # "none"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRole, crashes>>

-----------------------------------------------------------------------------
Progress(n) ==
    \/ AddLearner(n) \/ StartEtcd(n) \/ Promote(n) \/ Activate(n)
    \/ RemoveVoter(n) \/ StopEtcd(n) \/ RemoveLearner(n)
    \/ LeaveCluster(n) \/ FinishTeardown(n)

Operator(n) ==
    \/ McJoin(n) \/ BeginJoin(n) \/ FailJoin(n) \/ BeginRemove(n)
    \/ ForceLeaveCluster(n)

Env(n) == ConfirmMember(n) \/ McRollback(n) \/ Crash(n) \/ Restart(n)

Next == Rebalance \/ \E n \in Nodes : Progress(n) \/ Operator(n) \/ Env(n)

Fairness ==
    /\ WF_vars(Rebalance)
    /\ \A n \in Nodes :
        /\ WF_vars(AddLearner(n))    /\ WF_vars(StartEtcd(n))
        /\ WF_vars(Promote(n))       /\ WF_vars(Activate(n))
        /\ WF_vars(RemoveVoter(n))   /\ WF_vars(StopEtcd(n))
        /\ WF_vars(RemoveLearner(n)) /\ WF_vars(LeaveCluster(n))
        /\ WF_vars(FinishTeardown(n))
        /\ WF_vars(ConfirmMember(n)) /\ WF_vars(McRollback(n))
        /\ WF_vars(Restart(n))

Spec == Init /\ [][Next]_vars /\ Fairness

-----------------------------------------------------------------------------
(* Safety                                                                  *)

TypeOK ==
    /\ nodeStatus  \in [Nodes -> Phases]
    /\ lease       \in Nodes \cup {NoNode}
    /\ mcRole      \in [Nodes -> {"none", "pending", "member"}]
    /\ dqliteRole  \in [Nodes -> {"none", "spare", "voter"}]
    /\ etcdRole    \in [Nodes -> {"none", "learner", "voter"}]
    /\ etcdRunning \in [Nodes -> BOOLEAN]
    /\ up          \in [Nodes -> BOOLEAN]
    /\ crashes     \in 0..MaxCrashes

\* Every in-flight transition is owned by the lease holder ...
LeaseOwnsTransition ==
    \A n \in Nodes : nodeStatus[n] \in Transitional => lease = n

\* ... hence no two nodes are ever mid-transition at the same time.
SingleTransition ==
    Cardinality({n \in Nodes : nodeStatus[n] \in Transitional}) <= 1

AtMostOneLearner == Cardinality(Learners) <= 1

LearnerHoldsLease == \A n \in Learners : lease = n

\* Active only after etcd promotion and dqlite membership were committed.
ActiveIsConfirmedVoter ==
    \A n \in Nodes :
        nodeStatus[n] = "Active" => etcdRole[n] = "voter" /\ mcRole[n] = "member"

\* No etcd state outside a transition. Absent nodes may be microcluster
\* members that have not taken the lease yet (see AdmissionsResolve).
AbsentIsClean ==
    \A n \in Nodes :
        nodeStatus[n] = "Absent" => etcdRole[n] = "none" /\ ~etcdRunning[n]

\* Every etcd member is still a microcluster member, so k8sd can reach it
\* again to remove it. Violated by #161.
NoOrphanedEtcdMember ==
    \A n \in Nodes : etcdRole[n] # "none" => mcRole[n] = "member"

DqliteRoleMatchesMembership ==
    \A n \in Nodes : (dqliteRole[n] # "none") <=> (mcRole[n] = "member")

ControlPlaneNeverEmpty == Voters # {} /\ DqliteVoters # {}

\* At most one orchestrator step is enabled per node in every reachable
\* state, so the Go planner (lifecycle.Plan) can be a pure function.
ProgressDeterministic ==
    \A n \in Nodes :
        LET en == << ENABLED AddLearner(n),    ENABLED StartEtcd(n),
                     ENABLED Promote(n),       ENABLED Activate(n),
                     ENABLED RemoveVoter(n),   ENABLED StopEtcd(n),
                     ENABLED RemoveLearner(n), ENABLED LeaveCluster(n),
                     ENABLED FinishTeardown(n) >>
        IN Cardinality({i \in 1..9 : en[i]}) <= 1

\* A node becomes Active only in a step where both quorums confirm it, and
\* never together with another node.
ActivationRequiresQuorum ==
    [][\A n \in Nodes :
        (nodeStatus[n] # "Active" /\ nodeStatus'[n] = "Active") =>
            /\ EtcdQuorum /\ DqliteQuorum
            /\ EtcdQuorum' /\ DqliteQuorum'
            /\ \A m \in Nodes \ {n} : nodeStatus'[m] = nodeStatus[m]
    ]_vars

\* Membership reconfigurations never take a quorate cluster to a non-quorate one.
EtcdReconfigPreservesQuorum ==
    [][Voters' # Voters => (EtcdQuorum /\ EtcdQuorum')]_vars

DqliteReconfigPreservesQuorum ==
    [][DqliteVoters' # DqliteVoters => (DqliteQuorum /\ DqliteQuorum')]_vars

\* Only the environment (crashes) may cost quorum; orchestrator steps never do.
OrchestratorNeverBreaksQuorum ==
    [][(up' = up /\ EtcdQuorum) => EtcdQuorum']_vars

-----------------------------------------------------------------------------
(* Liveness                                                                *)

TransitionsTerminate ==
    \A n \in Nodes :
        nodeStatus[n] \in Transitional ~> nodeStatus[n] \in {"Active", "Absent"}

\* A microcluster member without a journal row either takes the lease or is
\* rolled back.
AdmissionsResolve ==
    \A n \in Nodes :
        (nodeStatus[n] = "Absent" /\ mcRole[n] # "none")
            ~> (mcRole[n] = "none" \/ nodeStatus[n] # "Absent")

LeaseEventuallyFree == []<>(lease = NoNode)

=============================================================================
