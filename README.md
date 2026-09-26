# task26092705

`reqv` — 需求验证状态引擎（纯内存 Go 库，调用方串行使用）。

## 用法

```go
e := reqv.New()
e.AddRequirement("REQ-1")
e.AddRequirement("REQ-2")
e.AddEdge("REQ-1", "REQ-2")          // REQ-2 是 REQ-1 的派生需求
e.LinkTestCase("REQ-2", "TC-1")      // 测试用例按 ID 引用即存在
e.RecordExecution("EX-1", "TC-1", 1700000000, reqv.ResultPass)
e.CreateAnomaly("AN-1", 1, []string{"REQ-1"}, nil)
e.TransitionAnomaly("AN-1", reqv.AnomalyUnderInvestigation)

st, gap, err := e.Requirement("REQ-1") // 派生状态 + 覆盖缺口
sum := e.Summary()                     // 四种状态计数 + 缺口计数
```

## 设计

- 每条需求维护一组计数器（活跃 1 级异常数、异常命中的用例数、fail/pass
  用例数、各状态的派生需求数、缺口派生需求数），状态与缺口由计数器 O(1)
  推出；查询只读已维护好的结果。
- 写操作先校验、后修改，非法操作不留任何可观察改动。局部计数器更新后
  沿「派生 → 上级」方向传播，仅在状态或缺口真正变化时继续，且每个节点
  每次传播只处理一次（菱形共享不会指数放大）。
- 执行记录按 `(at, seq)` 存入每个测试用例的 treap（优先级为键的确定性
  哈希），插入/删除/取当前结果都是 O(log n)，删除不随记录数线性增长。
- 加边成环检查为从派生端出发的可达性 DFS，只访问下游子图。

## 测试

- `TestDifferential`：固定种子的随机差分测试，混合非法操作，逐步与
  从零重算的参考实现比对全部状态、缺口与汇总；生成器覆盖菱形共享、
  双路命中同一需求的 1 级异常撤掉一路、删除最新执行记录、先记录/建
  异常后关联、Resolved 后重开等情形。
- `TestDiamond40`：40 层 × 2 节点相邻层全连接 DAG 的耗时测试（底层
  写入与成环加边均在微秒级）。
- `TestScale`：10 万需求、30 万关联、1 万次随机写在百毫秒内完成。

运行：`go test ./...`（`TestScale` 在 `-short` 模式下跳过）。
