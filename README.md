# reqstate

需求验证状态引擎。纯内存 Go 库，调用方串行使用，无持久化、无并发。
为项目中的每条需求增量维护派生验证状态与覆盖缺口标记，所有查询 O(1)。

## 数据模型

- 需求：字符串 ID；「上级 → 派生」边构成 DAG（一个派生可挂多个上级）。
- 测试用例：ID 被引用即存在，与需求多对多。
- 执行记录：`(execID, testCaseID, at, result)`，result ∈ pass/fail/incomplete。
  当前结果取 `at` 最大者，并列时后记录者胜；记录可乱序补录、可作废删除。
  每个测试用例的执行记录存于 treap（按 `(at, seq)` 排序），增删 O(log n)。
- 异常：`(id, 严重度 1–4, 关联需求集合, 关联测试用例集合)`，关联创建时固定。
  状态机 Open → UnderInvestigation → Resolved → Closed，另允许 Resolved → Open。
  Open/UnderInvestigation 为活跃态。

## 派生状态（自上而下取第一条成立）

1. **Blocked**：活跃 1 级异常直接关联本需求或关联了其当前关联的测试用例；或任一派生为 Blocked。
2. **Failed**：某关联测试用例当前结果为 fail；或任一派生为 Failed。
3. **Verified**：至少有一个关联测试用例或派生；关联测试用例当前结果全为 pass 且派生全为 Verified。
4. **Pending**。

**覆盖缺口**：本需求无关联测试用例，且所有派生（若有）均为缺口。

## 增量维护

每条需求持有聚合计数器：各状态派生数、缺口派生数、关联测试用例中
pass/fail 数、命中本需求的活跃 1 级异常（按通道计数：直接关联一路、
每个关联测试用例一路，同一异常双路命中只算一次但撤一路仍生效）。
写操作只更新局部计数器并沿父链传播实际发生的状态/缺口变化；
汇总计数（四状态 + 缺口）同步增减。查询只读已维护好的结果。

非法操作（ID 未知或重复、重复关联、删除不存在的边、加边成环、非法迁移）
先校验后变更，返回错误且不留下任何可观察的改动。

## API

```go
eng := reqstate.New()
eng.AddRequirement("R1")
eng.AddEdge("R1", "R2")              // R1 是 R2 的上级
eng.LinkTestCase("R2", "TC1")
eng.RecordExecution("E1", "TC1", 42, reqstate.Pass)
eng.CreateAnomaly("A1", 1, []string{"R2"}, []string{"TC1"})
eng.TransitionAnomaly("A1", reqstate.UnderInvestigation)
state, gap, err := eng.Requirement("R1")
sum := eng.Summary()                 // Blocked/Failed/Verified/Pending/Gaps
```

## 测试

- `differential_test.go`：固定种子随机差分测试，逐步与从零重算的参考实现
  （`reference_test.go`）比对所有需求状态、缺口与汇总；操作序列夹杂非法操作。
- `coverage_test.go`：断言生成器确实造出菱形共享、双路异常撤一路、
  删除最新执行记录、先执行/异常后关联、Resolved 重开等情形。
- `scenarios_test.go`：上述情形的确定性脚本测试及非法操作原子性测试。
- `perf_test.go`：40 层 × 2 节点全连接 DAG 的底层写与成环加边（微秒级）；
  5 万条记录下删除执行记录；10 万需求 / 30 万关联上 1 万次随机写（约 0.5s）。

```sh
go test ./...
```
