# Node lifecycle specification

Formal model of the control-plane join/remove lifecycle implemented by
[`pkg/k8sd/lifecycle`](..).

| File | Purpose |
| ---- | ------- |
| `NodeLifecycle.tla` | TLA+ spec: variables, `Init`, `Next`, invariants, temporal properties |
| `NodeLifecycle.cfg` | 3 nodes, 2 crashes: all properties must hold |
| `NodeLifecycle_stop_before_remove.cfg` | Mutation: stop etcd before removing a voter. TLC must find a counterexample |
| `NodeLifecycle_unguarded_remove.cfg` | Mutation: remove a voter without a quorum check. TLC must find a counterexample |
| `NodeLifecycle_force_remove.cfg` | Mutation: `remove-node --force` drops a node from microcluster when its etcd removal failed (#161). TLC must find a counterexample |
| `NodeLifecyclePlusCal.tla` / `.cfg` | PlusCal rendering, one `either` branch per `lifecycle.Step` |

What the model covers:

- **dqlite roles.** dqlite keeps an odd number of voters (at most 3). Other members are spares, so a 2-node cluster has 1 dqlite voter and 1 spare, while etcd has 2 voters. A voter leaves either because quorum survives without it, or by handing its vote to an online spare first.
- **Hook order.** `k8s join-cluster` makes the node a microcluster member before the PostJoin hook takes the k8sd lease. A join that fails before that is rolled back by microcluster.
- **Node-local actions.** Starting and stopping etcd run on the node itself and need that host up.

Run all checks (needs Java 11+):

```sh
./check.sh
```

After editing the PlusCal algorithm, regenerate its translation with
`java -cp tla2tools.jar pcal.trans NodeLifecyclePlusCal.tla`.

Every change to `lifecycle.Plan` must keep `TestPlanVectors` aligned with the
guards in `NodeLifecycle.tla`.
