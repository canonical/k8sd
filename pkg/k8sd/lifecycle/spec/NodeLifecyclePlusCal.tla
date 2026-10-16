------------------------ MODULE NodeLifecyclePlusCal ------------------------
(***************************************************************************)
(* PlusCal rendering of NodeLifecycle.tla for developer review.            *)
(*                                                                         *)
(* Each node runs one level-triggered Reconciler: every `either` branch is *)
(* one atomic, guarded step and corresponds 1:1 to a lifecycle.Step in Go. *)
(* Operator (join/remove/fail requests) and Crasher are unfair; Rebooter   *)
(* is weakly fair, i.e. crashed hosts eventually come back.                *)
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
    McMembers    == {n \in Nodes : mcRole[n] = "member"}
    DqliteQuorum == Majority({n \in McMembers : up[n]}, McMembers)
    DqliteQuorumWithout(n) ==
        LET M == McMembers \ {n} IN M # {} /\ Majority({m \in M : up[m]}, M)

    SingleTransition ==
        Cardinality({n \in Nodes : nodeStatus[n] \in Transitional}) <= 1
    AtMostOneLearner == Cardinality(Learners) <= 1
    ActiveIsConfirmedVoter ==
        \A n \in Nodes :
            nodeStatus[n] = "Active" => etcdRole[n] = "voter" /\ mcRole[n] = "member"
    AbsentIsClean ==
        \A n \in Nodes :
            nodeStatus[n] = "Absent" =>
                mcRole[n] = "none" /\ etcdRole[n] = "none" /\ ~etcdRunning[n]
    ControlPlaneNeverEmpty == Voters # {}
end define;

fair process Reconciler \in Nodes
begin
Reconcile:
    while TRUE do
        either \* ConfirmMember: microcluster PENDING -> member
            await nodeStatus[self] = "Joining" /\ mcRole[self] = "pending"
                  /\ up[self] /\ DqliteQuorum;
            mcRole[self] := "member";
        or \* AddLearner
            await nodeStatus[self] = "Joining" /\ mcRole[self] = "member"
                  /\ etcdRole[self] = "none" /\ Learners = {} /\ EtcdQuorum;
            etcdRole[self] := "learner";
        or \* StartEtcd
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
        or \* StopEtcd: never while still a voter
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
                  /\ (mcRole[self] = "member" => DqliteQuorumWithout(self));
            mcRole[self] := "none";
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
        either \* BeginJoin
            await nodeStatus[self[2]] = "Absent" /\ up[self[2]]
                  /\ lease = NoNode /\ DqliteQuorum;
            lease := self[2];
            nodeStatus[self[2]] := "Joining";
            mcRole[self[2]] := "pending";
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
VARIABLES nodeStatus, lease, mcRole, etcdRole, etcdRunning, up, crashes

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
McMembers    == {n \in Nodes : mcRole[n] = "member"}
DqliteQuorum == Majority({n \in McMembers : up[n]}, McMembers)
DqliteQuorumWithout(n) ==
    LET M == McMembers \ {n} IN M # {} /\ Majority({m \in M : up[m]}, M)

SingleTransition ==
    Cardinality({n \in Nodes : nodeStatus[n] \in Transitional}) <= 1
AtMostOneLearner == Cardinality(Learners) <= 1
ActiveIsConfirmedVoter ==
    \A n \in Nodes :
        nodeStatus[n] = "Active" => etcdRole[n] = "voter" /\ mcRole[n] = "member"
AbsentIsClean ==
    \A n \in Nodes :
        nodeStatus[n] = "Absent" =>
            mcRole[n] = "none" /\ etcdRole[n] = "none" /\ ~etcdRunning[n]
ControlPlaneNeverEmpty == Voters # {}


vars == << nodeStatus, lease, mcRole, etcdRole, etcdRunning, up, crashes >>

ProcSet == (Nodes) \cup ({<<"op", n>> : n \in Nodes}) \cup ({<<"crash", n>> : n \in Nodes}) \cup ({<<"boot", n>> : n \in Nodes})

Init == (* Global variables *)
        /\ nodeStatus = [n \in Nodes |-> IF n = Seed THEN "Active" ELSE "Absent"]
        /\ lease = NoNode
        /\ mcRole = [n \in Nodes |-> IF n = Seed THEN "member" ELSE "none"]
        /\ etcdRole = [n \in Nodes |-> IF n = Seed THEN "voter" ELSE "none"]
        /\ etcdRunning = [n \in Nodes |-> n = Seed]
        /\ up = [n \in Nodes |-> TRUE]
        /\ crashes = 0

Reconciler(self) == /\ \/ /\ nodeStatus[self] = "Joining" /\ mcRole[self] = "pending"
                             /\ up[self] /\ DqliteQuorum
                          /\ mcRole' = [mcRole EXCEPT ![self] = "member"]
                          /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning>>
                       \/ /\ nodeStatus[self] = "Joining" /\ mcRole[self] = "member"
                             /\ etcdRole[self] = "none" /\ Learners = {} /\ EtcdQuorum
                          /\ etcdRole' = [etcdRole EXCEPT ![self] = "learner"]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRunning>>
                       \/ /\ nodeStatus[self] = "Joining" /\ etcdRole[self] # "none"
                             /\ up[self] /\ ~etcdRunning[self]
                          /\ etcdRunning' = [etcdRunning EXCEPT ![self] = TRUE]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole>>
                       \/ /\ nodeStatus[self] = "Joining" /\ etcdRole[self] = "learner"
                             /\ up[self] /\ etcdRunning[self] /\ EtcdQuorum
                          /\ etcdRole' = [etcdRole EXCEPT ![self] = "voter"]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRunning>>
                       \/ /\ nodeStatus[self] = "Joining" /\ etcdRole[self] = "voter"
                             /\ mcRole[self] = "member" /\ up[self] /\ etcdRunning[self]
                             /\ EtcdQuorum /\ DqliteQuorum
                          /\ nodeStatus' = [nodeStatus EXCEPT ![self] = "Active"]
                          /\ lease' = NoNode
                          /\ UNCHANGED <<mcRole, etcdRole, etcdRunning>>
                       \/ /\ nodeStatus[self] \in Teardown /\ etcdRole[self] = "voter"
                             /\ EtcdQuorum /\ EtcdQuorumWithout(self)
                          /\ etcdRole' = [etcdRole EXCEPT ![self] = "none"]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRunning>>
                       \/ /\ nodeStatus[self] \in Teardown /\ up[self] /\ etcdRunning[self]
                             /\ etcdRole[self] # "voter"
                          /\ etcdRunning' = [etcdRunning EXCEPT ![self] = FALSE]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRole>>
                       \/ /\ nodeStatus[self] \in Teardown /\ etcdRole[self] = "learner"
                             /\ ~etcdRunning[self] /\ EtcdQuorum
                          /\ etcdRole' = [etcdRole EXCEPT ![self] = "none"]
                          /\ UNCHANGED <<nodeStatus, lease, mcRole, etcdRunning>>
                       \/ /\ nodeStatus[self] \in Teardown /\ etcdRole[self] = "none"
                             /\ ~etcdRunning[self] /\ mcRole[self] # "none" /\ DqliteQuorum
                             /\ (mcRole[self] = "member" => DqliteQuorumWithout(self))
                          /\ mcRole' = [mcRole EXCEPT ![self] = "none"]
                          /\ UNCHANGED <<nodeStatus, lease, etcdRole, etcdRunning>>
                       \/ /\ nodeStatus[self] \in Teardown /\ mcRole[self] = "none"
                             /\ etcdRole[self] = "none" /\ ~etcdRunning[self] /\ DqliteQuorum
                          /\ nodeStatus' = [nodeStatus EXCEPT ![self] = "Absent"]
                          /\ lease' = NoNode
                          /\ UNCHANGED <<mcRole, etcdRole, etcdRunning>>
                    /\ UNCHANGED << up, crashes >>

Operator(self) == /\ \/ /\ nodeStatus[self[2]] = "Absent" /\ up[self[2]]
                           /\ lease = NoNode /\ DqliteQuorum
                        /\ lease' = self[2]
                        /\ nodeStatus' = [nodeStatus EXCEPT ![self[2]] = "Joining"]
                        /\ mcRole' = [mcRole EXCEPT ![self[2]] = "pending"]
                     \/ /\ nodeStatus[self[2]] = "Joining" /\ DqliteQuorum
                        /\ nodeStatus' = [nodeStatus EXCEPT ![self[2]] = "RollingBack"]
                        /\ UNCHANGED <<lease, mcRole>>
                     \/ /\ nodeStatus[self[2]] = "Active" /\ lease = NoNode
                           /\ DqliteQuorum /\ Cardinality(Voters) > 1
                        /\ lease' = self[2]
                        /\ nodeStatus' = [nodeStatus EXCEPT ![self[2]] = "Removing"]
                        /\ UNCHANGED mcRole
                  /\ UNCHANGED << etcdRole, etcdRunning, up, crashes >>

Crasher(self) == /\ up[self[2]] /\ crashes < MaxCrashes
                 /\ up' = [up EXCEPT ![self[2]] = FALSE]
                 /\ etcdRunning' = [etcdRunning EXCEPT ![self[2]] = FALSE]
                 /\ crashes' = crashes + 1
                 /\ UNCHANGED << nodeStatus, lease, mcRole, etcdRole >>

Rebooter(self) == /\ ~up[self[2]]
                  /\ up' = [up EXCEPT ![self[2]] = TRUE]
                  /\ etcdRunning' = [etcdRunning EXCEPT ![self[2]] = etcdRole[self[2]] # "none"]
                  /\ UNCHANGED << nodeStatus, lease, mcRole, etcdRole, crashes >>

Next == (\E self \in Nodes: Reconciler(self))
           \/ (\E self \in {<<"op", n>> : n \in Nodes}: Operator(self))
           \/ (\E self \in {<<"crash", n>> : n \in Nodes}: Crasher(self))
           \/ (\E self \in {<<"boot", n>> : n \in Nodes}: Rebooter(self))

Spec == /\ Init /\ [][Next]_vars
        /\ \A self \in Nodes : WF_vars(Reconciler(self))
        /\ \A self \in {<<"boot", n>> : n \in Nodes} : WF_vars(Rebooter(self))

\* END TRANSLATION

TransitionsTerminate ==
    \A n \in Nodes :
        nodeStatus[n] \in Transitional ~> nodeStatus[n] \in {"Active", "Absent"}

=============================================================================
