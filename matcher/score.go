package matcher

// scale 为各单项分的满分值。
const scale = 10000

// skillScore 计算技能分：命中的所需技能数除以所需技能总数，再乘 10000 向下取整。
// 调用方保证 required > 0 且 matched <= required。
func skillScore(matched, required int) uint64 {
	return uint64(matched) * scale / uint64(required)
}

// priceScore 计算价格分：预计金额在预算闭区间内比例为 1；
// 低于下限时为预计金额/下限，高于上限时为上限/预计金额；比例乘 10000 向下取整。
// 调用方保证 budgetMin > 0 且 cost > 0。
func priceScore(cost, budgetMin, budgetMax uint64) uint64 {
	switch {
	case cost < budgetMin:
		return cost * scale / budgetMin
	case cost > budgetMax:
		return budgetMax * scale / cost
	default:
		return scale
	}
}

// ratingScore 计算口碑分：星级总和/(条数*5)*10000 向下取整；没有评价时为 0。
func ratingScore(ratingSum uint32, ratingCount uint16) uint64 {
	if ratingCount == 0 {
		return 0
	}
	return uint64(ratingSum) * scale / (uint64(ratingCount) * 5)
}

// statusScore 计算状态分：可接单 10000、忙碌 5000、休假 0。
func statusScore(status Status) uint64 {
	switch status {
	case StatusAvailable:
		return scale
	case StatusBusy:
		return scale / 2
	default:
		return 0
	}
}
