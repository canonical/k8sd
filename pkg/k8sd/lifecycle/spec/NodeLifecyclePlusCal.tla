------------------------ MODULE NodeLifecyclePlusCal ------------------------
(***************************************************************************)
(* PlusCal rendering of NodeLifecycle.tla for developer review.            *)
(*                                                                         *)
(* Each node runs one level-triggered Reconciler: every `either` branch is *)
(* one atomic, guarded step and corresponds 1:1 to a lifecycle.Step in Go. *)
(* Operator (join/remove/fail requests) and Crasher are unfair. Membership *)
(* (microcluster confirm/rollback), Rebalancer (dqlite roles) and Rebooter *)
(* are weakly fair, i.e. crashed hosts eventually come back.               *)
(*                                                                         *)
(* Regenerate the translation after editing the algorithm:                 *)
(*   java -cp tla2tools.jar pcal.trans NodeLifecyclePlusCal.tla            *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Nodes, Seed, NoNode, MaxCrashes

ASSUME Seed \in Nodes /\ NoNode \notin Nodes /\ MaxCrashes \in Nat

(* --algorithm NodeLifecyclePC

variables
    nodeStatus  = [n \in Nodes |-> IF n = Seed THEN "Active" ELSE "Absent"],
    lease       = NoNode,
    mcRole      = [n \in Nodes |-> IF n = Seed THEN "member" ELSE "none"],
    dqliteRole  = [n \in Nodes |-> IF n = Seed THEN "voter" ELSE "none"],
    etcdRole    = [n \in Nodes |-> IF n = Seed THEN "voter" ELSE "none"],
    etcdRunning = [n \in Nodes |-> n = Seed],
    up          = [n \in Nodes |-> TRUE],
    crashes     = 0;

define
    Transitional == {"Joining", "RollingBack", "Removing"}
    Teardown     == {"RollingBack", "Removing"}

    Majority(live, all) == 2 * Cardinality(live) > Cardinality(all)
    Voters       == {n \in Nodes : etcdRole[n] = "voter"}
    Learners     == {n \in Nodes : etcdRole[n] = "learner"}
    LiveVoters   == {n \in Voters : up[n] /\ etcdRunning[n]}
    EtcdQuorum   == Majority(LiveVoters, Voters)
    EtcdQuorumWithout(n) ==
        LET V == Voters \ {n} IN V # {} /\ Majority(LiveVoters \ {n}, V)
    DqliteVoters == {n \in Nodes : dqliteRole[n] = "voter"}
    DqliteQuorum == Majority({n \in DqliteVoters : up[n]}, DqliteVoters)
    DqliteQuorumWithout(n) ==
        LET V == DqliteVoters \ {n} IN V # {} /\ Majority({m \in V : up[m]}, V)
    \* Online spares that can take over n's dqlite vote before n leaves.
    HandoverTargets(n) == {m \in Nodes \ {n} : dqliteRole[m] = "spare" /\ up[m]}
    CanLeaveDqlite(n) ==
        dqliteRole[n] # "voter" \/ DqliteQuorumWithout(n) \/ HandoverTargets(n) # {}

    SingleTransition ==
        Cardinality({n \in Nodes : nodeStatus[n] \in Transitional}) <= 1
    AtMostOneLearner == Cardinality(Learners) <= 1
    ActiveIsConfirmedVoter ==
        \A n \in Nodes :
            nodeStatus[n] = "Active" => etcdRole[n] = "voter" /\ mcRole[n] = "member"
    AbsentIsClean ==
        \A n \in Nodes :
            nodeStatus[n] = "Absent" => etcdRole[n] = "none" /\ ~etcdRunning[n]
    NoOrphanedEtcdMember ==
        \A n \in Nodes : etcdRole[n] # "none" => mcRole[n] = "member"
    DqliteRoleMatchesMembership ==
        \A n \in Nodes : (dqliteRole[n] # "none") <=> (mcRole[n] = "member")
    ControlPlaneNeverEmpty == Voters # {} /\ DqliteVoters # {}
end define;

\* Removes n from dqlite, handing its vote to an online spare when needed.
macro leave_dqlite(n) begin
    if dqliteRole[n] = "voter" /\ ~DqliteQuorumWithout(n) then
        with m \in HandoverTargets(n) do
            dqliteRole := [dqliteRole EXCEPT ![n] = "none", ![m] = "voter"];
        end with;
    else
        dqliteRole[n] := "none";
    end if;
end macro;

fair process Reconciler \in Nodes
begin
Reconcile:
    while TRUE do
        either \* AddLearner
            await nodeStatus[self] = "Joining" /\ mcRole[self] = "member"
                  /\ etcdRole[self] = "none" /\ Learners = {} /\ EtcdQuorum;
            etcdRole[self] := "learner";
        or \* StartEtcd (node-local)
            await nodeStatus[self] = "Joining" /\ etcdRole[self] # "none"
                  /\ up[self] /\ ~etcdRunning[self];
            etcdRunning[self] := TRUE;
        or \* Promote
            await nodeStatus[self] = "Joining" /\ etcdRole[self] = "learner"
                  /\ up[self] /\ etcdRunning[self] /\ EtcdQuorum;
            etcdRole[self] := "voter";
        or \* Activate: journal commit + lease release in one dqlite tx
            await nodeStatus[self] = "Joining" /\ etcdRole[self] = "voter"
                  /\ mcRole[self] = "member" /\ up[self] /\ etcdRunning[self]
                  /\ EtcdQuorum /\ DqliteQuorum;
            nodeStatus[self] := "Active";
            lease := NoNode;
        or \* RemoveVoter: before stopping etcd, only if quorum survives
            await nodeStatus[self] \in Teardown /\ etcdRole[self] = "voter"
                  /\ EtcdQuorum /\ EtcdQuorumWithout(self);
            etcdRole[self] := "none";
        or \* StopEtcd (node-local): never while still a voter
            await nodeStatus[self] \in Teardown /\ up[self] /\ etcdRunning[self]
                  /\ etcdRole[self] # "voter";
            etcdRunning[self] := FALSE;
        or \* RemoveLearner
            await nodeStatus[self] \in Teardown /\ etcdRole[self] = "learner"
                  /\ ~etcdRunning[self] /\ EtcdQuorum;
            etcdRole[self] := "none";
        or \* LeaveCluster: microcluster RemoveClusterMember
            await nodeStatus[self] \in Teardown /\ etcdRole[self] = "none"
                  /\ ~etcdRunning[self] /\ mcRole[self] # "none" /\ DqliteQuorum
                  /\ CanLeaveDqlite(self);
            mcRole[self] := "none";
            leave_dqlite(self);
        or \* FinishTeardown
            await nodeStatus[self] \in Teardown /\ mcRole[self] = "none"
                  /\ etcdRole[self] = "none" /\ ~etcdRunning[self] /\ DqliteQuorum;
            nodeStatus[self] := "Absent";
            lease := NoNode;
        end either;
    end while;
end process;

process Operator \in {<<"op", n>> : n \in Nodes}
begin
Request:
    while TRUE do
        either \* McJoin: `k8s join-cluster`, microcluster PENDING, no lease
            await nodeStatus[self[2]] = "Absent" /\ mcRole[self[2]] = "none"
                  /\ etcdRole[self[2]] = "none" /\ ~etcdRunning[self[2]]
                  /\ up[self[2]] /\ DqliteQuorum;
            mcRole[self[2]] := "pending";
        or \* BeginJoin: PostJoin hook takes the lease
            await nodeStatus[self[2]] = "Absent" /\ mcRole[self[2]] = "member"
                  /\ up[self[2]] /\ lease = NoNode /\ DqliteQuorum;
            lease := self[2];
            nodeStatus[self[2]] := "Joining";
        or \* FailJoin
            await nodeStatus[self[2]] = "Joining" /\ DqliteQuorum;
            nodeStatus[self[2]] := "RollingBack";
        or \* BeginRemove
            await nodeStatus[self[2]] = "Active" /\ lease = NoNode
                  /\ DqliteQuorum /\ Cardinality(Voters) > 1;
            lease := self[2];
            nodeStatus[self[2]] := "Removing";
        end either;
    end while;
end process;

fair process Membership \in {<<"mc", n>> : n \in Nodes}
begin
Member:
    while TRUE do
        either \* ConfirmMember: PENDING -> member, as a dqlite spare
            await mcRole[self[2]] = "pending" /\ up[self[2]] /\ DqliteQuorum;
            mcRole[self[2]] := "member";
            dqliteRole[self[2]] := "spare";
        or \* McRollback: join failed before the lease, microcluster force-deletes
            await nodeStatus[self[2]] = "Absent" /\ mcRole[self[2]] # "none"
                  /\ etcdRole[self[2]] = "none" /\ ~etcdRunning[self[2]]
                  /\ DqliteQuorum /\ CanLeaveDqlite(self[2]);
            mcRole[self[2]] := "none";
            leave_dqlite(self[2]);
        end either;
    end while;
end process;

fair process Rebalancer = "rebalance"
begin
Rebalance:
    while TRUE do
        either \* 1 -> 3 voters
            with a \in Nodes, b \in Nodes do
                await a # b /\ Cardinality(DqliteVoters) = 1 /\ DqliteQuorum
                      /\ dqliteRole[a] = "spare" /\ dqliteRole[b] = "spare"
                      /\ up[a] /\ up[b];
                dqliteRole := [dqliteRole EXCEPT ![a] = "voter", ![b] = "voter"];
            end with;
        or \* replace an offline voter with an online spare
            with o \in Nodes, n \in Nodes do
                await dqliteRole[o] = "voter" /\ ~up[o] /\ dqliteRole[n] = "spare"
                      /\ up[n] /\ DqliteQuorum;
                dqliteRole := [dqliteRole EXCEPT ![o] = "spare", ![n] = "voter"];
            end with;
        or \* 2 -> 1 voters, keep the count odd
            with v \in Nodes do
                await Cardinality(DqliteVoters) = 2 /\ dqliteRole[v] = "voter" /\ DqliteQuorum;
                dqliteRole[v] := "spare";
            end with;
        end either;
    end while;
end process;

process Crasher \in {<<"crash", n>> : n \in Nodes}
begin
Crash:
    while TRUE do
        await up[self[2]] /\ crashes < MaxCrashes;
        up[self[2]] := FALSE;
        etcdRunning[self[2]] := FALSE;
        crashes := crashes + 1;
    end while;
end process;

fair process Rebooter \in {<<"boot", n>> : n \in Nodes}
begin
Boot:
    while TRUE do
        await ~up[self[2]];
        up[self[2]] := TRUE;
        etcdRunning[self[2]] := etcdRole[self[2]] # "none";
    end while;
end process;

end algorithm; *)
\* BEGIN TRANSLATION
VARIABLES nodeStatus, lease, mcRole, dqliteRole, etcdRole, etcdRunning, up, 
          crashes

(* define statement *)
Transitional == {"Joining", "RollingBack", "Removing"}
Teardown     == {"RollingBack", "Removing"}

Majority(live, all) == 2 * Cardinality(live) > Cardinality(all)
Voters       == {n \in Nodes : etcdRole[n] = "voter"}
Learners     == {n \in Nodes : etcdRole[n] = "learner"}
LiveVoters   == {n \in Voters : up[n] /\ etcdRunning[n]}
EtcdQuorum   == Majority(LiveVoters, Voters)
EtcdQuorumWithout(n) ==
    LET V == Voters \ {n} IN V # {} /\ Majority(LiveVoters \ {n}, V)
DqliteVoters == {n \in Nodes : dqliteRole[n] = "voter"}
DqliteQuorum == Majority({n \in DqliteVoters : up[n]}, DqliteVoters)
DqliteQuorumWithout(n) ==
    LET V == DqliteVoters \ {n} IN V # {} /\ Majority({m \in V : up[m]}, V)

HandoverTargets(n) == {m \in Nodes \ {n} : dqliteRole[m] = "spare" /\ up[m]}
CanLeaveDqlite(n) ==
    dqliteRole[n] # "voter" \/ DqliteQuorumWithout(n) \/ HandoverTargets(n) # {}

SingleTransition ==
    Cardinality({n \in Nodes : nodeStatus[n] \in Transitional}) <= 1
AtMostOneLearner == Cardinality(Learners) <= 1
ActiveIsConfirmedVoter ==
    \A n \in Nodes :
        nodeStatus[n] = "Active" => etcdRole[n] = "voter" /\ mcRole[n] = "member"
AbsentIsClean ==
    \A n \in Nodes :
        nodeStatus[n] = "Absent" => etcdRole[n] = "none" /\ ~etcdRunning[n]
NoOrphanedEtcdMember ==
    \A n \in Nodes : etcdRole[n] # "none" => mcRole[n] = "member"
DqliteRoleMatchesMembership ==
    \A n \in Nodes : (dqliteRole[n] # "none") <=> (mcRole[n] = "member")
ControlPlaneNeverEmpty == Voters # {} /\ DqliteVoters # {}


vars == << nodeStatus, lease, mcRole, dqliteRole, etcdRole, etcdRunning, up, 
           crashes >>

ProcSet == (Nodes) \cup ({<<"op", n>> : n \in Nodes}) \cup ({<<"mc", n>> : n \in Nodes}) \cup {"rebalance"} \cup ({<<"crash", n>> : n \in Nodes}) \cup ({<<"boot", n>> : n \in Nodes})

Init == (* Global variables *)
        /\ nodeStatus = [n \in Nodes |-> IF n = Seed THEN "Active" ELSE "Absent"]
        /\ lease = NoNode
        /\ mcRole = [n \in Nodes |-> IF n = Seed THEN "member" ELSE "none"]
        /\ dqliteRole = [n \in Nodes |-> IF n = Seed THEN "voter" ELSE "none"]
        /\ etcdRole = [n \in Nodes |-> IF n = Seed THEN "voter" ELSE "none"]
        /\ etcdRunning = [n \in Nodes |-> n = Seed]
        /\ up = [n \in Nodes |-> TRUE]
        /\ crashes = 0

Reconciler(self) == /\ \/ /\ nodeStatus[self] = "Joining" /\ mcRole[self] = "member"
                             /\ etcdRole[self] = "none" /\ Learners = {} /\ EtcdQuorum
                          /\ etcdRole' = [etcdRole EXCEPT ![self] = "learner"]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRunning>>
                       \/ /\ nodeStatus[self] = "Joining" /\ etcdRole[self] # "none"
                             /\ up[self] /\ ~etcdRunning[self]
                          /\ etcdRunning' = [etcdRunning EXCEPT ![self] = TRUE]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRole>>
                       \/ /\ nodeStatus[self] = "Joining" /\ etcdRole[self] = "learner"
                             /\ up[self] /\ etcdRunning[self] /\ EtcdQuorum
                          /\ etcdRole' = [etcdRole EXCEPT ![self] = "voter"]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRunning>>
                       \/ /\ nodeStatus[self] = "Joining" /\ etcdRole[self] = "voter"
                             /\ mcRole[self] = "member" /\ up[self] /\ etcdRunning[self]
                             /\ EtcdQuorum /\ DqliteQuorum
                          /\ nodeStatus' = [nodeStatus EXCEPT ![self] = "Active"]
                          /\ lease' = NoNode
                          /\ UNCHANGED <<mcRole, dqliteRole, etcdRole, etcdRunning>>
                       \/ /\ nodeStatus[self] \in Teardown /\ etcdRole[self] = "voter"
                             /\ EtcdQuorum /\ EtcdQuorumWithout(self)
                          /\ etcdRole' = [etcdRole EXCEPT ![self] = "none"]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRunning>>
                       \/ /\ nodeStatus[self] \in Teardown /\ up[self] /\ etcdRunning[self]
                             /\ etcdRole[self] # "voter"
                          /\ etcdRunning' = [etcdRunning EXCEPT ![self] = FALSE]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRole>>
                       \/ /\ nodeStatus[self] \in Teardown /\ etcdRole[self] = "learner"
                             /\ ~etcdRunning[self] /\ EtcdQuorum
                          /\ etcdRole' = [etcdRole EXCEPT ![self] = "none"]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, dqliteRole, etcdRunning>>
                       \/ /\ nodeStatus[self] \in Teardown /\ etcdRole[self] = "none"
                             /\ ~etcdRunning[self] /\ mcRole[self] # "none" /\ DqliteQuorum
                             /\ CanLeaveDqlite(self)
                          /\ mcRole' = [mcRole EXCEPT ![self] = "none"]
                          /\ IF dqliteRole[self] = "voter" /\ ~DqliteQuorumWithout(self)
                                THEN /\ \E m \in HandoverTargets(self):
                                          dqliteRole' = [dqliteRole EXCEPT ![self] = "none", ![m] = "voter"]
                                ELSE /\ dqliteRole' = [dqliteRole EXCEPT ![self] = "none"]
                          /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning>>
                       \/ /\ nodeStatus[self] \in Teardown /\ mcRole[self] = "none"
                             /\ etcdRole[self] = "none" /\ ~etcdRunning[self] /\ DqliteQuorum
                          /\ nodeStatus' = [nodeStatus EXCEPT ![self] = "Absent"]
                          /\ lease' = NoNode
                          /\ UNCHANGED <<mcRole, dqliteRole, etcdRole, etcdRunning>>
                    /\ UNCHANGED << up, crashes >>

Operator(self) == /\ \/ /\ nodeStatus[self[2]] = "Absent" /\ mcRole[self[2]] = "none"
                           /\ etcdRole[self[2]] = "none" /\ ~etcdRunning[self[2]]
                           /\ up[self[2]] /\ DqliteQuorum
                        /\ mcRole' = [mcRole EXCEPT ![self[2]] = "pending"]
                        /\ UNCHANGED <<nodeStatus, lease>>
                     \/ /\ nodeStatus[self[2]] = "Absent" /\ mcRole[self[2]] = "member"
                           /\ up[self[2]] /\ lease = NoNode /\ DqliteQuorum
                        /\ lease' = self[2]
                        /\ nodeStatus' = [nodeStatus EXCEPT ![self[2]] = "Joining"]
                        /\ UNCHANGED mcRole
                     \/ /\ nodeStatus[self[2]] = "Joining" /\ DqliteQuorum
                        /\ nodeStatus' = [nodeStatus EXCEPT ![self[2]] = "RollingBack"]
                        /\ UNCHANGED <<lease, mcRole>>
                     \/ /\ nodeStatus[self[2]] = "Active" /\ lease = NoNode
                           /\ DqliteQuorum /\ Cardinality(Voters) > 1
                        /\ lease' = self[2]
                        /\ nodeStatus' = [nodeStatus EXCEPT ![self[2]] = "Removing"]
                        /\ UNCHANGED mcRole
                  /\ UNCHANGED << dqliteRole, etcdRole, etcdRunning, up, 
                                  crashes >>

Membership(self) == /\ \/ /\ mcRole[self[2]] = "pending" /\ up[self[2]] /\ DqliteQuorum
                          /\ mcRole' = [mcRole EXCEPT ![self[2]] = "member"]
                          /\ dqliteRole' = [dqliteRole EXCEPT ![self[2]] = "spare"]
                       \/ /\ nodeStatus[self[2]] = "Absent" /\ mcRole[self[2]] # "none"
                             /\ etcdRole[self[2]] = "none" /\ ~etcdRunning[self[2]]
                             /\ DqliteQuorum /\ CanLeaveDqlite(self[2])
                          /\ mcRole' = [mcRole EXCEPT ![self[2]] = "none"]
                          /\ IF dqliteRole[(self[2])] = "voter" /\ ~DqliteQuorumWithout((self[2]))
                                THEN /\ \E m \in HandoverTargets((self[2])):
                                          dqliteRole' = [dqliteRole EXCEPT ![(self[2])] = "none", ![m] = "voter"]
                                ELSE /\ dqliteRole' = [dqliteRole EXCEPT ![(self[2])] = "none"]
                    /\ UNCHANGED << nodeStatus, lease, etcdRole, etcdRunning, 
                                    up, crashes >>

Rebalancer == /\ \/ /\ \E a \in Nodes:
                         \E b \in Nodes:
                           /\ a # b /\ Cardinality(DqliteVoters) = 1 /\ DqliteQuorum
                              /\ dqliteRole[a] = "spare" /\ dqliteRole[b] = "spare"
                              /\ up[a] /\ up[b]
                           /\ dqliteRole' = [dqliteRole EXCEPT ![a] = "voter", ![b] = "voter"]
                 \/ /\ \E o \in Nodes:
                         \E n \in Nodes:
                           /\ dqliteRole[o] = "voter" /\ ~up[o] /\ dqliteRole[n] = "spare"
                              /\ up[n] /\ DqliteQuorum
                           /\ dqliteRole' = [dqliteRole EXCEPT ![o] = "spare", ![n] = "voter"]
                 \/ /\ \E v \in Nodes:
                         /\ Cardinality(DqliteVoters) = 2 /\ dqliteRole[v] = "voter" /\ DqliteQuorum
                         /\ dqliteRole' = [dqliteRole EXCEPT ![v] = "spare"]
              /\ UNCHANGED << nodeStatus, lease, mcRole, etcdRole, etcdRunning, 
                              up, crashes >>

Crasher(self) == /\ up[self[2]] /\ crashes < MaxCrashes
                 /\ up' = [up EXCEPT ![self[2]] = FALSE]
                 /\ etcdRunning' = [etcdRunning EXCEPT ![self[2]] = FALSE]
                 /\ crashes' = crashes + 1
                 /\ UNCHANGED << nodeStatus, lease, mcRole, dqliteRole, 
                                 etcdRole >>

Rebooter(self) == /\ ~up[self[2]]
                  /\ up' = [up EXCEPT ![self[2]] = TRUE]
                  /\ etcdRunning' = [etcdRunning EXCEPT ![self[2]] = etcdRole[self[2]] # "none"]
                  /\ UNCHANGED << nodeStatus, lease, mcRole, dqliteRole, 
                                  etcdRole, crashes >>

Next == Rebalancer
           \/ (\E self \in Nodes: Reconciler(self))
           \/ (\E self \in {<<"op", n>> : n \in Nodes}: Operator(self))
           \/ (\E self \in {<<"mc", n>> : n \in Nodes}: Membership(self))
           \/ (\E self \in {<<"crash", n>> : n \in Nodes}: Crasher(self))
           \/ (\E self \in {<<"boot", n>> : n \in Nodes}: Rebooter(self))

Spec == /\ Init /\ [][Next]_vars
        /\ \A self \in Nodes : WF_vars(Reconciler(self))
        /\ \A self \in {<<"mc", n>> : n \in Nodes} : WF_vars(Membership(self))
        /\ WF_vars(Rebalancer)
        /\ \A self \in {<<"boot", n>> : n \in Nodes} : WF_vars(Rebooter(self))

\* END TRANSLATION

TransitionsTerminate ==
    \A n \in Nodes :
        nodeStatus[n] \in Transitional ~> nodeStatus[n] \in {"Active", "Absent"}

AdmissionsResolve ==
    \A n \in Nodes :
        (nodeStatus[n] = "Absent" /\ mcRole[n] # "none")
            ~> (mcRole[n] = "none" \/ nodeStatus[n] # "Absent")

=============================================================================
