------------------------- MODULE EtcdNativeDowngrade -------------------------
(***************************************************************************)
(* etcd 3.6 <-> 3.7 refreshes driven by snap hooks, per k8s-snap proposal *)
(* 003-etcd-native-downgrade (decision matrix rows 3-6, 8; prestart-check *)
(* guard; configure-hook arm-before-fail).                                 *)
(*                                                                         *)
(* Abstractions:                                                           *)
(*  - clusterCV / dgEnabled are the raft-committed values; cv[m] / sv[m]   *)
(*    are what member m has applied to its local backend.                  *)
(*  - snapd refresh: pre-refresh hook -> stop services -> link (binary     *)
(*    swap) -> start services -> configure. Undo relinks the old binary   *)
(*    and runs no hooks.                                                   *)
(*  - etcd panics (MustDetectDowngrade) when a running member's binary is  *)
(*    older than its local cluster version, at startup or on apply.        *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    Members,
    \* Row 4 waits for storage; downgrades re-verify after local etcd stops.
    FixRow4,
    \* prestart-check reads the local backend's cv offline, not a peer's cv.
    FixGuard,
    \* Subset of {"configure", "service"}: injected 6->7 refresh failures.
    FailureModes,
    \* Allow etcd to crash at any time (enables quorum loss).
    AllowCrashes,
    \* Arm-before-fail stops local etcd and re-verifies cv before exiting.
    FixArm,
    \* Arm-before-fail may run out of its ~30 s budget at any step.
    ArmTimeouts

Old == 6
New == 7
Versions == {Old, New}

PCs == {"none", "hook", "waiting", "ready", "verify", "stopped",
        "starting", "configuring", "arming", "armwait", "armverify"}

VARIABLES
    binary,     \* installed etcd binary per member
    running,    \* etcd process up
    cv,         \* locally applied cluster version
    sv,         \* local storage version
    clusterCV,  \* committed cluster version
    dgEnabled,  \* committed DowngradeInfo.Enabled (target is always Old)
    pc,         \* per-node snap refresh progress
    tgt         \* per-node refresh target

vars == <<binary, running, cv, sv, clusterCV, dgEnabled, pc, tgt>>

TypeOK ==
    /\ binary \in [Members -> Versions]
    /\ running \in [Members -> BOOLEAN]
    /\ cv \in [Members -> Versions]
    /\ sv \in [Members -> Versions]
    /\ clusterCV \in Versions
    /\ dgEnabled \in BOOLEAN
    /\ pc \in [Members -> PCs]
    /\ tgt \in [Members -> Versions]

Quorum == 2 * Cardinality({m \in Members : running[m]}) > Cardinality(Members)

\* Version row 4 compares against; the fix uses a linearizable read.
SeenCV(m) == IF FixRow4 THEN clusterCV ELSE cv[m]

\* prestart-check: binary < cv -> refuse to start; unreachable -> fail open.
GuardPasses(m) ==
    LET peers == {n \in Members \ {m} : running[n]} IN
    IF FixGuard
    THEN /\ binary[m] >= cv[m]
         /\ (Quorum => binary[m] >= clusterCV)
    ELSE peers = {} \/ \E n \in peers : binary[m] >= cv[n]

Init ==
    /\ binary = [m \in Members |-> Old]
    /\ running = [m \in Members |-> TRUE]
    /\ cv = [m \in Members |-> Old]
    /\ sv = [m \in Members |-> Old]
    /\ clusterCV = Old
    /\ dgEnabled = FALSE
    /\ pc = [m \in Members |-> "none"]
    /\ tgt = [m \in Members |-> Old]

-----------------------------------------------------------------------------
(* snapd refresh *)

BeginRefresh(m, t) ==
    /\ pc[m] = "none"
    /\ t # binary[m]
    /\ pc' = [pc EXCEPT ![m] = "hook"]
    /\ tgt' = [tgt EXCEPT ![m] = t]
    /\ UNCHANGED <<binary, running, cv, sv, clusterCV, dgEnabled>>

\* Row 3: forward refresh cancels an armed downgrade; never blocks.
HookUpgrade(m) ==
    /\ pc[m] = "hook"
    /\ tgt[m] > binary[m]
    /\ dgEnabled' = IF dgEnabled /\ running[m] /\ Quorum THEN FALSE ELSE dgEnabled
    /\ pc' = [pc EXCEPT ![m] = "ready"]
    /\ UNCHANGED <<binary, running, cv, sv, clusterCV, tgt>>

HookDowngrade(m) ==
    /\ pc[m] = "hook"
    /\ tgt[m] < binary[m]
    /\ IF ~running[m] THEN                                       \* row 8
            /\ pc' = [pc EXCEPT ![m] = "none"]
            /\ UNCHANGED <<dgEnabled, clusterCV>>
       ELSE IF SeenCV(m) = tgt[m] THEN                           \* row 4
            /\ pc' = [pc EXCEPT ![m] = IF FixRow4 THEN "waiting" ELSE "ready"]
            /\ UNCHANGED <<dgEnabled, clusterCV>>
       ELSE IF dgEnabled THEN                                    \* row 5
            /\ pc' = [pc EXCEPT ![m] = "waiting"]
            /\ UNCHANGED <<dgEnabled, clusterCV>>
       ELSE IF Quorum /\ clusterCV = tgt[m] + 1 THEN             \* row 6
            /\ dgEnabled' = TRUE
            /\ clusterCV' = tgt[m]
            /\ pc' = [pc EXCEPT ![m] = "waiting"]
       ELSE                                                      \* validate fails
            /\ pc' = [pc EXCEPT ![m] = "none"]
            /\ UNCHANGED <<dgEnabled, clusterCV>>
    /\ UNCHANGED <<binary, running, cv, sv, tgt>>

\* Step f: poll every member's Status until storage version == target.
WaitStorage(m) ==
    /\ pc[m] = "waiting"
    /\ \A n \in Members : running[n] /\ sv[n] = tgt[m]
    /\ pc' = [pc EXCEPT ![m] = "ready"]
    /\ UNCHANGED <<binary, running, cv, sv, clusterCV, dgEnabled, tgt>>

\* 8-minute budget exceeded: hook fails, snapd aborts the refresh.
HookTimeout(m) ==
    /\ pc[m] = "waiting"
    /\ pc' = [pc EXCEPT ![m] = "none"]
    /\ UNCHANGED <<binary, running, cv, sv, clusterCV, dgEnabled, tgt>>

StopServices(m) ==
    /\ pc[m] = "ready"
    /\ running' = [running EXCEPT ![m] = FALSE]
    /\ pc' = [pc EXCEPT ![m] =
            IF FixRow4 /\ tgt[m] < binary[m] THEN "verify" ELSE "stopped"]
    /\ UNCHANGED <<binary, cv, sv, clusterCV, dgEnabled, tgt>>

\* Fix: with local etcd down the leader cannot raise cv, so this check is stable.
VerifyStopped(m) ==
    /\ pc[m] = "verify"
    /\ IF Quorum /\ clusterCV = tgt[m] /\ cv[m] = tgt[m] /\ sv[m] = tgt[m]
         THEN /\ pc' = [pc EXCEPT ![m] = "stopped"]
              /\ UNCHANGED running
         ELSE /\ pc' = [pc EXCEPT ![m] = "none"]
              /\ running' = [running EXCEPT ![m] = TRUE]
    /\ UNCHANGED <<binary, cv, sv, clusterCV, dgEnabled, tgt>>

LinkSnap(m) ==
    /\ pc[m] = "stopped"
    /\ binary' = [binary EXCEPT ![m] = tgt[m]]
    /\ pc' = [pc EXCEPT ![m] =
            IF FailureModes # {} /\ tgt[m] > binary[m] THEN "starting" ELSE "none"]
    /\ UNCHANGED <<running, cv, sv, clusterCV, dgEnabled, tgt>>

StartServices(m) ==
    /\ pc[m] = "starting"
    /\ running' = [running EXCEPT ![m] = TRUE]
    /\ pc' = [pc EXCEPT ![m] = "configuring"]
    /\ UNCHANGED <<binary, cv, sv, clusterCV, dgEnabled, tgt>>

ConfigureOK(m) ==
    /\ pc[m] = "configuring"
    /\ pc' = [pc EXCEPT ![m] = "none"]
    /\ UNCHANGED <<binary, running, cv, sv, clusterCV, dgEnabled, tgt>>

\* snapd undo: stop services, relink the old revision, no hooks run.
Undo(m) ==
    /\ binary' = [binary EXCEPT ![m] = Old]
    /\ running' = [running EXCEPT ![m] = FALSE]
    /\ pc' = [pc EXCEPT ![m] = "none"]
    /\ UNCHANGED <<cv, sv, clusterCV, dgEnabled, tgt>>

\* A service failing at start-snap-services; configure never runs.
ServiceFail(m) ==
    /\ "service" \in FailureModes
    /\ pc[m] = "configuring"
    /\ Undo(m)

ConfigureFail(m) ==
    /\ "configure" \in FailureModes
    /\ pc[m] = "configuring"
    /\ pc' = [pc EXCEPT ![m] = "arming"]
    /\ UNCHANGED <<binary, running, cv, sv, clusterCV, dgEnabled, tgt>>

\* Arm-before-fail: best-effort validate + enable towards Old.
ArmDowngrade(m) ==
    /\ pc[m] = "arming"
    /\ IF running[m] /\ Quorum /\ ~dgEnabled /\ clusterCV = New
         THEN /\ dgEnabled' = TRUE
              /\ clusterCV' = Old
         ELSE UNCHANGED <<dgEnabled, clusterCV>>
    /\ pc' = [pc EXCEPT ![m] = "armwait"]
    /\ UNCHANGED <<binary, running, cv, sv, tgt>>

\* Storage wait done. As proposed the hook exits now; the fix stops etcd first.
ArmReady(m) ==
    /\ pc[m] = "armwait"
    /\ \A n \in Members : running[n] /\ sv[n] = Old
    /\ IF FixArm
         THEN /\ cv[m] = Old
              /\ running' = [running EXCEPT ![m] = FALSE]
              /\ pc' = [pc EXCEPT ![m] = "armverify"]
              /\ UNCHANGED <<binary, cv, sv, clusterCV, dgEnabled, tgt>>
         ELSE Undo(m)

\* With local etcd down cv cannot rise; otherwise restart and re-arm.
ArmVerify(m) ==
    /\ pc[m] = "armverify"
    /\ IF Quorum /\ clusterCV = Old /\ cv[m] = Old
         THEN Undo(m)
         ELSE /\ running' = [running EXCEPT ![m] = TRUE]
              /\ pc' = [pc EXCEPT ![m] = "arming"]
              /\ UNCHANGED <<binary, cv, sv, clusterCV, dgEnabled, tgt>>

\* Fix: cv rose after an arm that had nothing to do; arm again.
ArmRetry(m) ==
    /\ FixArm
    /\ pc[m] = "armwait"
    /\ ~dgEnabled
    /\ clusterCV = New
    /\ pc' = [pc EXCEPT ![m] = "arming"]
    /\ UNCHANGED <<binary, running, cv, sv, clusterCV, dgEnabled, tgt>>

ArmTimeout(m) ==
    /\ ArmTimeouts
    /\ pc[m] \in {"arming", "armwait", "armverify"}
    /\ Undo(m)

-----------------------------------------------------------------------------
(* etcd *)

Start(m) ==
    /\ ~running[m]
    /\ pc[m] \notin {"verify", "stopped", "starting", "armverify"}
    /\ GuardPasses(m)
    /\ running' = [running EXCEPT ![m] = TRUE]
    /\ UNCHANGED <<binary, cv, sv, clusterCV, dgEnabled, pc, tgt>>

Crash(m) ==
    /\ AllowCrashes
    /\ running[m]
    /\ running' = [running EXCEPT ![m] = FALSE]
    /\ UNCHANGED <<binary, cv, sv, clusterCV, dgEnabled, pc, tgt>>

ApplyClusterVersion(m) ==
    /\ running[m]
    /\ cv[m] # clusterCV
    /\ cv' = [cv EXCEPT ![m] = clusterCV]
    /\ UNCHANGED <<binary, running, sv, clusterCV, dgEnabled, pc, tgt>>

\* UpdateStorageVersionIfNeeded, run by each member's version monitor.
MigrateStorage(m) ==
    /\ running[m]
    /\ sv[m] # cv[m]
    /\ sv' = [sv EXCEPT ![m] = cv[m]]
    /\ UNCHANGED <<binary, running, cv, clusterCV, dgEnabled, pc, tgt>>

\* Leader lifts cv once every member runs the newer binary.
RaiseClusterVersion ==
    /\ ~dgEnabled
    /\ clusterCV = Old
    /\ \A n \in Members : running[n] /\ binary[n] = New
    /\ clusterCV' = New
    /\ UNCHANGED <<binary, running, cv, sv, dgEnabled, pc, tgt>>

\* CancelDowngradeIfNeeded once every member runs the target.
FinishDowngrade ==
    /\ dgEnabled
    /\ \A n \in Members : running[n] /\ binary[n] = clusterCV
    /\ dgEnabled' = FALSE
    /\ UNCHANGED <<binary, running, cv, sv, clusterCV, pc, tgt>>

-----------------------------------------------------------------------------

Next ==
    \/ \E m \in Members, t \in Versions : BeginRefresh(m, t)
    \/ \E m \in Members :
        \/ HookUpgrade(m)
        \/ HookDowngrade(m)
        \/ WaitStorage(m)
        \/ HookTimeout(m)
        \/ StopServices(m)
        \/ VerifyStopped(m)
        \/ LinkSnap(m)
        \/ StartServices(m)
        \/ ConfigureOK(m)
        \/ ServiceFail(m)
        \/ ConfigureFail(m)
        \/ ArmDowngrade(m)
        \/ ArmReady(m)
        \/ ArmVerify(m)
        \/ ArmRetry(m)
        \/ ArmTimeout(m)
        \/ Start(m)
        \/ Crash(m)
        \/ ApplyClusterVersion(m)
        \/ MigrateStorage(m)
    \/ RaiseClusterVersion
    \/ FinishDowngrade

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* Properties *)

\* MustDetectDowngrade never fires on a running member.
NoDowngradePanic == \A m \in Members : running[m] => binary[m] >= cv[m]

\* Narrower: no member is started on a binary its local backend rejects.
StartupSafe ==
    [][\A m \in Members : (~running[m] /\ running'[m]) => binary[m] >= cv[m]]_vars

\* Upstream: storage version is migrated before the old binary is linked.
StorageMigratedBeforeSwap ==
    \A m \in Members :
        (pc[m] = "stopped" /\ tgt[m] < binary[m]) => sv[m] = tgt[m]

\* No idle member is left with a binary its own backend rejects.
NoStrandedMember ==
    \A m \in Members : (pc[m] = "none" /\ ~running[m]) => binary[m] >= cv[m]

=============================================================================
