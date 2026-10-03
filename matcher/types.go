// Package matcher 提供 GigNexus 人才匹配库：加载候选人静态快照并按权重查询前 20 名匹配结果。
package matcher

// Status 表示候选人接单状态。
type Status uint8

const (
	StatusAvailable Status = iota // 可接单
	StatusBusy                    // 忙碌
	StatusOnLeave                 // 休假
)

// Candidate 为一位自由职业者的静态资料快照。
type Candidate struct {
	ID          int32    // 唯一编号
	Skills      []uint16 // 技能编号，最多 20 个，按编号精确匹配，不计重复
	HourlyRate  uint32   // 时薪，美分，正整数
	Status      Status   // 接单状态
	RatingSum   uint32   // 最近 50 条评价星级总和（不足 50 条时为全部评价）
	RatingCount uint16   // 评价条数，0 表示没有评价
}

// Weights 为四项权重，非负整数且合计必须为 100。
type Weights struct {
	Skill  uint32
	Price  uint32
	Rating uint32
	Status uint32
}

// DefaultWeights 返回默认权重：技能 40、价格 25、口碑 25、状态 10。
func DefaultWeights() Weights {
	return Weights{Skill: 40, Price: 25, Rating: 25, Status: 10}
}

// Query 为一次匹配查询。
type Query struct {
	Skills    []uint16 // 所需技能集合，非空
	Hours     uint32   // 预计工时，正整数
	BudgetMin uint64   // 预算下限，美分，正数
	BudgetMax uint64   // 预算上限，美分，正数，不小于下限
	Weights   Weights  // 四项权重，合计 100
	MinScore  uint64   // 最低最终分
}

// Match 为一条匹配结果。
type Match struct {
	CandidateID int32
	Score       uint64 // 最终分，范围 0 至 1000000
}
