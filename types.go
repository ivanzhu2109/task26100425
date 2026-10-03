package talentmatch

// Status 表示候选人的接单状态。
type Status int32

const (
	StatusAvailable Status = iota // 可接单
	StatusBusy                    // 忙碌
	StatusVacation                // 休假
)

// 权重维度下标。
const (
	WeightSkill = iota
	WeightPrice
	WeightReputation
	WeightStatus
	weightDimCount
)

// DefaultWeights 为默认权重：技能、价格、口碑、状态依次 40/25/25/10。
var DefaultWeights = [weightDimCount]uint32{40, 25, 25, 10}

// Candidate 是一位自由职业者的静态资料快照。
type Candidate struct {
	ID              int64   // 唯一整数编号
	Skills          []int32 // 技能编号，最多 20 个，按编号精确匹配，不计重复
	HourlyRateCents int64   // 以美分计的正整数时薪
	Status          Status  // 可接单 / 忙碌 / 休假
	RatingSum       int64   // 最近 50 条评价（不足 50 条时为全部评价）的星级总和
	RatingCount     int32   // 上述评价的条数，没有评价时为 0
}

// Query 是一次匹配查询。
type Query struct {
	RequiredSkills []int32                // 非空的所需技能集合
	EstimatedHours int64                  // 正整数预计工时
	BudgetMinCents int64                  // 预算下限（美分），正数
	BudgetMaxCents int64                  // 预算上限（美分），正数，不低于下限
	Weights        [weightDimCount]uint32 // 四项权重，非负且合计 100
	MinScore       int64                  // 最低分，最终分不低于该值才返回
}

// Match 是一条匹配结果。
type Match struct {
	CandidateID int64
	Score       int64 // 最终分，范围 0 至 1000000
}
