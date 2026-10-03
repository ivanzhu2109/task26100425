package talentmatch

// 各分项满分与状态分取值。
const (
	subScoreMax   int64 = 10000
	statusBusy    int64 = 5000
	finalScoreMax       = subScoreMax * 100 // 权重合计 100，最终分上限 1000000
)

// scoreCandidate 计算候选人在给定查询下的四项整数分与最终分。
//
// 技能分：命中的所需技能数 / 所需技能总数 * 10000，向下取整。
// 价格分：预计金额 = 时薪 * 预计工时；在预算闭区间内比例为 1，
// 低于下限为 预计金额/下限，高于上限为 上限/预计金额，再乘 10000 向下取整。
// 口碑分：星级总和 / (条数*5) * 10000，向下取整；无评价为 0。
// 状态分：可接单 / 忙碌 / 休假 依次 10000 / 5000 / 0。
// 最终分为四项整数分分别乘对应权重后相加，范围 0..1000000，不再舍入。
//
// required 为所需技能集合；c.Skills 须已去重（由 Load 保证）。
func scoreCandidate(c *Candidate, q *Query, required map[int32]struct{}) (skill, price, reputation, status, final int64) {
	var matched int64
	for _, s := range c.Skills {
		if _, ok := required[s]; ok {
			matched++
		}
	}
	skill = matched * subScoreMax / int64(len(required))

	amount := c.HourlyRateCents * q.EstimatedHours
	switch {
	case amount < q.BudgetMinCents:
		price = amount * subScoreMax / q.BudgetMinCents
	case amount > q.BudgetMaxCents:
		price = q.BudgetMaxCents * subScoreMax / amount
	default:
		price = subScoreMax
	}

	if c.RatingCount > 0 {
		reputation = c.RatingSum * subScoreMax / (int64(c.RatingCount) * 5)
	}

	switch c.Status {
	case StatusAvailable:
		status = subScoreMax
	case StatusBusy:
		status = statusBusy
	}

	final = skill*int64(q.Weights[WeightSkill]) +
		price*int64(q.Weights[WeightPrice]) +
		reputation*int64(q.Weights[WeightReputation]) +
		status*int64(q.Weights[WeightStatus])
	return skill, price, reputation, status, final
}
