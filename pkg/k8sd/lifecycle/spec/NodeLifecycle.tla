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
(* Network partitions are modelled as crashes: from a quorum point of view *)
(* an unreachable node is indistinguishable from a dead one.               *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    Nodes,        \* candidate control-plane hosts
    Seed,         \* bootstrapped node, initially Active
    NoNode,       \* sentinel: lease not held
    MaxCrashes,   \* bound on crash events so liveness is checkable
    Mutation      \* "none" | "stop-before-remove" | "unguarded-remove"

ASSUME /\ Seed \in Nodes
       /\ NoNode \notin Nodes
       /\ MaxCrashes \in Nat
       /\ Mutation \in {"none", "stop-before-remove", "unguarded-remove"}

Phases       == {"Absent", "Joining", "Active", "RollingBack", "Removing"}
Transitional == {"Joining", "RollingBack", "Removing"}
Teardown     == {"RollingBack", "Removing"}

VARIABLES
    nodeStatus,   \* [Nodes -> Phases]  durable journal row (dqlite)
    lease,        \* Nodes \cup {NoNode} cluster-wide transition lease (dqlite)
    mcRole,       \* [Nodes -> {"none","pending","member"}] microcluster membership
    etcdRole,     \* [Nodes -> {"none","learner","voter"}]  etcd raft membership
    etcdRunning,  \* [Nodes -> BOOLEAN] local etcd process
    up,           \* [Nodes -> BOOLEAN] host alive and reachable
    crashes       \* number of crash events so far

vars == <<nodeStatus, lease, mcRole, etcdRole, etcdRunning, up, crashes>>

-----------------------------------------------------------------------------
(* Derived consensus state                                                 *)

Majority(live, all) == 2 * Cardinality(live) > Cardinality(all)

Voters     == {n \in Nodes : etcdRole[n] = "voter"}
Learners   == {n \in Nodes : etcdRole[n] = "learner"}
LiveVoters == {n \in Voters : up[n] /\ etcdRunning[n]}
EtcdQuorum == Majority(LiveVoters, Voters)

EtcdQuorumWithout(n) ==
    LET V == Voters \ {n} IN V # {} /\ Majority(LiveVoters \ {n}, V)

\* Abstraction: every microcluster member is a dqlite voter (exact for <= 3).
McMembers    == {n \in Nodes : mcRole[n] = "member"}
DqliteQuorum == Majority({n \in McMembers : up[n]}, McMembers)

DqliteQuorumWithout(n) ==
    LET M == McMembers \ {n} IN M # {} /\ Majority({m \in M : up[m]}, M)

-----------------------------------------------------------------------------
Init ==
    /\ nodeStatus  = [n \in Nodes |-> IF n = Seed THEN "Active" ELSE "Absent"]
    /\ lease       = NoNode
    /\ mcRole      = [n \in Nodes |-> IF n = Seed THEN "member" ELSE "none"]
    /\ etcdRole    = [n \in Nodes |-> IF n = Seed THEN "voter" ELSE "none"]
    /\ etcdRunning = [n \in Nodes |-> n = Seed]
    /\ up          = [n \in Nodes |-> TRUE]
    /\ crashes     = 0

-----------------------------------------------------------------------------
(* Join path                                                               *)

\* POST /cluster/join: one dqlite tx acquires the lease and writes the
\* journal row; microcluster records the node as PENDING.
BeginJoin(n) ==
    /\ nodeStatus[n] = "Absent"
    /\ up[n]
    /\ lease = NoNode
    /\ DqliteQuorum
    /\ lease'      = n
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "Joining"]
    /\ mcRole'     = [mcRole EXCEPT ![n] = "pending"]
    /\ UNCHANGED <<etcdRole, etcdRunning, up, crashes>>

\* microcluster finishes the dqlite join: PENDING -> member.
ConfirmMember(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ mcRole[n] = "pending"
    /\ up[n]
    /\ DqliteQuorum
    /\ mcRole' = [mcRole EXCEPT ![n] = "member"]
    /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning, up, crashes>>

\* MemberAddAsLearner. etcd allows one learner and needs a leader.
AddLearner(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ mcRole[n] = "member"
    /\ etcdRole[n] = "none"
    /\ Learners = {}
    /\ EtcdQuorum
    /\ etcdRole' = [etcdRole EXCEPT ![n] = "learner"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRunning, up, crashes>>

StartEtcd(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ etcdRole[n] # "none"
    /\ up[n]
    /\ ~etcdRunning[n]
    /\ etcdRunning' = [etcdRunning EXCEPT ![n] = TRUE]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole, up, crashes>>

\* MemberPromote. etcd rejects promotion of a learner that is not in sync.
Promote(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ etcdRole[n] = "learner"
    /\ up[n]
    /\ etcdRunning[n]
    /\ EtcdQuorum
    /\ etcdRole' = [etcdRole EXCEPT ![n] = "voter"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRunning, up, crashes>>

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
    /\ UNCHANGED <<mcRole, etcdRole, etcdRunning, up, crashes>>

\* Any join step may fail: timeout, promote retries exhausted, version skew.
FailJoin(n) ==
    /\ nodeStatus[n] = "Joining"
    /\ DqliteQuorum
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "RollingBack"]
    /\ UNCHANGED <<lease, mcRole, etcdRole, etcdRunning, up, crashes>>

-----------------------------------------------------------------------------
(* Remove path                                                             *)

BeginRemove(n) ==
    /\ nodeStatus[n] = "Active"
    /\ lease = NoNode
    /\ DqliteQuorum
    /\ Cardinality(Voters) > 1          \* never remove the last control plane
    /\ lease'      = n
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "Removing"]
    /\ UNCHANGED <<mcRole, etcdRole, etcdRunning, up, crashes>>

-----------------------------------------------------------------------------
(* Teardown, shared by RollingBack and Removing                            *)

\* A voter is removed FIRST, while its local etcd still counts toward quorum.
RemoveVoter(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ etcdRole[n] = "voter"
    /\ EtcdQuorum
    /\ Mutation = "unguarded-remove" \/ EtcdQuorumWithout(n)
    /\ etcdRole' = [etcdRole EXCEPT ![n] = "none"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRunning, up, crashes>>

StopEtcd(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ up[n]
    /\ etcdRunning[n]
    /\ Mutation = "stop-before-remove" \/ etcdRole[n] # "voter"
    /\ etcdRunning' = [etcdRunning EXCEPT ![n] = FALSE]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole, up, crashes>>

\* Learners never count toward quorum, so removal is always safe.
RemoveLearner(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ etcdRole[n] = "learner"
    /\ ~etcdRunning[n]
    /\ EtcdQuorum
    /\ etcdRole' = [etcdRole EXCEPT ![n] = "none"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRunning, up, crashes>>

\* microcluster RemoveClusterMember.
LeaveCluster(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ etcdRole[n] = "none"
    /\ ~etcdRunning[n]
    /\ mcRole[n] # "none"
    /\ DqliteQuorum
    /\ mcRole[n] = "member" => DqliteQuorumWithout(n)
    /\ mcRole' = [mcRole EXCEPT ![n] = "none"]
    /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning, up, crashes>>

FinishTeardown(n) ==
    /\ nodeStatus[n] \in Teardown
    /\ mcRole[n] = "none"
    /\ etcdRole[n] = "none"
    /\ ~etcdRunning[n]
    /\ DqliteQuorum
    /\ nodeStatus' = [nodeStatus EXCEPT ![n] = "Absent"]
    /\ lease'      = NoNode
    /\ UNCHANGED <<mcRole, etcdRole, etcdRunning, up, crashes>>

-----------------------------------------------------------------------------
(* Environment                                                             *)

Crash(n) ==
    /\ up[n]
    /\ crashes < MaxCrashes
    /\ up'          = [up EXCEPT ![n] = FALSE]
    /\ etcdRunning' = [etcdRunning EXCEPT ![n] = FALSE]
    /\ crashes'     = crashes + 1
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole>>

\* The etcd snap service stays enabled while the host is an etcd member.
Restart(n) ==
    /\ ~up[n]
    /\ up'          = [up EXCEPT ![n] = TRUE]
    /\ etcdRunning' = [etcdRunning EXCEPT ![n] = etcdRole[n] # "none"]
    /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole, crashes>>

-----------------------------------------------------------------------------
Progress(n) ==
    \/ ConfirmMember(n) \/ AddLearner(n) \/ StartEtcd(n) \/ Promote(n)
    \/ Activate(n)
    \/ RemoveVoter(n) \/ StopEtcd(n) \/ RemoveLearner(n)
    \/ LeaveCluster(n) \/ FinishTeardown(n)

Operator(n) == BeginJoin(n) \/ FailJoin(n) \/ BeginRemove(n)

Env(n) == Crash(n) \/ Restart(n)

Next == \E n \in Nodes : Progress(n) \/ Operator(n) \/ Env(n)

Fairness ==
    \A n \in Nodes :
        /\ WF_vars(ConfirmMember(n)) /\ WF_vars(AddLearner(n))
        /\ WF_vars(StartEtcd(n))     /\ WF_vars(Promote(n))
        /\ WF_vars(Activate(n))      /\ WF_vars(RemoveVoter(n))
        /\ WF_vars(StopEtcd(n))      /\ WF_vars(RemoveLearner(n))
        /\ WF_vars(LeaveCluster(n))  /\ WF_vars(FinishTeardown(n))
        /\ WF_vars(Restart(n))

Spec == Init /\ [][Next]_vars /\ Fairness

-----------------------------------------------------------------------------
(* Safety                                                                  *)

TypeOK ==
    /\ nodeStatus  \in [Nodes -> Phases]
    /\ lease       \in Nodes \cup {NoNode}
    /\ mcRole      \in [Nodes -> {"none", "pending", "member"}]
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

\* No orphaned etcd learners / dqlite rows / running etcd after teardown.
AbsentIsClean ==
    \A n \in Nodes :
        nodeStatus[n] = "Absent" =>
            /\ mcRole[n] = "none"
            /\ etcdRole[n] = "none"
            /\ ~etcdRunning[n]

ControlPlaneNeverEmpty == Voters # {}

\* At most one orchestrator step is enabled per node in every reachable
\* state, so the Go planner (lifecycle.Plan) can be a pure function.
ProgressDeterministic ==
    \A n \in Nodes :
        LET en == << ENABLED ConfirmMember(n), ENABLED AddLearner(n),
                     ENABLED StartEtcd(n),     ENABLED Promote(n),
                     ENABLED Activate(n),      ENABLED RemoveVoter(n),
                     ENABLED StopEtcd(n),      ENABLED RemoveLearner(n),
                     ENABLED LeaveCluster(n),  ENABLED FinishTeardown(n) >>
        IN Cardinality({i \in 1..10 : en[i]}) <= 1

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
    [][McMembers' # McMembers => (DqliteQuorum /\ DqliteQuorum')]_vars

\* Only the environment (crashes) may cost quorum; orchestrator steps never do.
OrchestratorNeverBreaksQuorum ==
    [][(up' = up /\ EtcdQuorum) => EtcdQuorum']_vars

-----------------------------------------------------------------------------
(* Liveness                                                                *)

TransitionsTerminate ==
    \A n \in Nodes :
        nodeStatus[n] \in Transitional ~> nodeStatus[n] \in {"Active", "Absent"}

LeaseEventuallyFree == []<>(lease = NoNode)

=============================================================================
