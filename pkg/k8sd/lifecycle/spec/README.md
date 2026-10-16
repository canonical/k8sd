# Node lifecycle specification

Formal model of the control-plane join/remove lifecycle implemented by
[`pkg/k8sd/lifecycle`](..).

| File | Purpose |
| ---- | ------- |
| `NodeLifecycle.tla` | TLA+ spec: variables, `Init`, `Next`, invariants, temporal properties |
| `NodeLifecycle.cfg` | 3 nodes, 2 crashes: all properties must hold |
| `NodeLifecycle_stop_before_remove.cfg` | Mutation: stop etcd before removing a voter. TLC must find a counterexample |
| `NodeLifecycle_unguarded_remove.cfg` | Mutation: remove a voter without a quorum check. TLC must find a counterexample |
| `NodeLifecyclePlusCal.tla` / `.cfg` | PlusCal rendering, one `either` branch per `lifecycle.Step` |

Run all checks (needs Java 11+):

```sh
./check.sh
```

After editing the PlusCal algorithm, regenerate its translation with
`java -cp tla2tools.jar pcal.trans NodeLifecyclePlusCal.tla`.

Every change to `lifecycle.Plan` must keep `TestPlanVectors` aligned with the
guards in `NodeLifecycle.tla`.
