# talentmatch

GigNexus 人才匹配库：从大量自由职业者中为客户选出最合适的前 20 人。
纯 Go 库，候选资料存于内存，由上层直接调用，无接口层、无外部依赖。

## 能力

- `Load`：加载静态资料快照（支持 500000 名候选人、10000 种技能），加载后资料不变。
- `Query`：按技能、价格、口碑、状态四项加权评分，返回最终分不低于最低分的前 20 人。

## 用法

```go
m := talentmatch.NewMatcher()
if err := m.Load(candidates); err != nil { /* 校验失败 */ }

q := &talentmatch.Query{
    RequiredSkills: []int32{101, 205, 77},
    EstimatedHours: 40,
    BudgetMinCents: 200000,   // 预算下限（美分）
    BudgetMaxCents: 400000,   // 预算上限（美分）
    Weights:        talentmatch.DefaultWeights, // 技能/价格/口碑/状态 = 40/25/25/10
    MinScore:       300000,
}
matches, err := m.Query(q) // 按最终分降序、同分按编号升序，最多 20 条
```

- `Weights` 四项非负且合计 100；全零等价于 `DefaultWeights`；允许单项为 0 或 100。
- 评分规则见 `scorer.go` 注释；结果与逐人完整评分严格一致，
  零技能重合者与休假者仍有资格上榜。
- `Load` 完成后可并发执行 `Query`。

## 测试

```sh
go test ./...                              # 正确性测试
go test -run TestPerfFullScale -v          # 全规模性能验收（见 PERFORMANCE.md）
```
