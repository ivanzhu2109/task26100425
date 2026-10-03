package matcher

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrInvalidQuery 表示查询参数不合法。
var ErrInvalidQuery = errors.New("matcher: invalid query")

// TopN 为返回的最大结果数。
const TopN = 20

// maxScore 为最终分上限：单项满分 10000 乘权重合计 100。
const maxScore = scale * 100

// skillSpaceSize 为技能编号空间大小（uint16），位图共 8 KiB。
const skillSpaceSize = 1 << 16

// maxPriceTableEntries 为价格贡献查表法允许的最大时薪跨度；
// 超过该跨度时退回逐人除法，保证任意合法分布结果准确。
const maxPriceTableEntries = 1 << 20

var queryBuffers = sync.Pool{
	New: func() any { return &queryBuffer{} },
}

// queryBuffer 为单次查询的临时空间，通过 sync.Pool 复用以避免堆分配。
type queryBuffer struct {
	skillMask    [skillSpaceSize / 64]uint64
	priceContrib []uint32
}

// Match 返回最终分不低于 MinScore 的前 20 名候选人，
// 按最终分降序、同分按候选人编号升序排列。
// 零技能重合及休假者同样参与评分与排序。
func (s *Store) Match(q Query) ([]Match, error) {
	required, err := normalizeQuery(q)
	if err != nil {
		return nil, err
	}

	buf := queryBuffers.Get().(*queryBuffer)
	defer queryBuffers.Put(buf)

	// 所需技能位图：逐人命中计数只需位测试，无分支。
	mask := buf.skillMask[:]
	for i := range mask {
		mask[i] = 0
	}
	for _, sk := range required {
		mask[sk>>6] |= 1 << (sk & 63)
	}

	// 技能贡献表：命中 m 项时的加权得分。
	var skillContrib [maxSkills + 1]uint64
	for m := 0; m <= len(required) && m <= maxSkills; m++ {
		skillContrib[m] = skillScore(m, len(required)) * uint64(q.Weights.Skill)
	}

	wRating := uint64(q.Weights.Rating)
	wStatus := uint64(q.Weights.Status)

	top := make([]Match, 0, TopN)
	worst := 0
	consider := func(id int32, score uint64) {
		if score < q.MinScore {
			return
		}
		m := Match{CandidateID: id, Score: score}
		if len(top) < TopN {
			top = append(top, m)
			if better(top[worst], m) {
				worst = len(top) - 1
			}
			return
		}
		if !better(m, top[worst]) {
			return
		}
		top[worst] = m
		worst = 0
		for j := 1; j < TopN; j++ {
			if better(top[worst], top[j]) {
				worst = j
			}
		}
	}

	n := len(s.ids)
	if rateSpan := uint64(s.maxRate) - uint64(s.minRate) + 1; n > 0 && rateSpan <= maxPriceTableEntries {
		// 查表路径：价格贡献仅取决于时薪，按查询预计算整表。
		if cap(buf.priceContrib) < int(rateSpan) {
			buf.priceContrib = make([]uint32, rateSpan)
		}
		table := buf.priceContrib[:rateSpan]
		for r := uint64(0); r < rateSpan; r++ {
			cost := (uint64(s.minRate) + r) * uint64(q.Hours)
			table[r] = uint32(priceScore(cost, q.BudgetMin, q.BudgetMax) * uint64(q.Weights.Price))
		}
		minRate := s.minRate
		for i := 0; i < n; i++ {
			st := &s.statics[i]
			score := uint64(table[st.hourlyRate-minRate]) +
				uint64(st.ratingScore)*wRating + uint64(st.statusScore)*wStatus
			if q.Weights.Skill != 0 {
				score += skillContrib[skillHits(s.skills[i*maxSkills:], s.skillCounts[i], mask)]
			}
			consider(s.ids[i], score)
		}
	} else {
		// 回退路径：时薪跨度过大时逐人计算价格分，结果与查表完全一致。
		for i := 0; i < n; i++ {
			st := &s.statics[i]
			cost := uint64(st.hourlyRate) * uint64(q.Hours)
			score := priceScore(cost, q.BudgetMin, q.BudgetMax)*uint64(q.Weights.Price) +
				uint64(st.ratingScore)*wRating + uint64(st.statusScore)*wStatus
			if q.Weights.Skill != 0 {
				score += skillContrib[skillHits(s.skills[i*maxSkills:], s.skillCounts[i], mask)]
			}
			consider(s.ids[i], score)
		}
	}

	sort.Slice(top, func(a, b int) bool { return better(top[a], top[b]) })
	return top, nil
}

// skillHits 统计候选人升序技能行中命中所需技能位图的项数。
func skillHits(row []uint16, count uint8, mask []uint64) int {
	hits := 0
	for j := 0; j < int(count); j++ {
		sk := row[j]
		hits += int(mask[sk>>6] >> (sk & 63) & 1)
	}
	return hits
}

// better 报告 a 是否应排在 b 前：最终分降序，同分按编号升序。
func better(a, b Match) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.CandidateID < b.CandidateID
}

// normalizeQuery 校验查询，并返回去重升序排列的所需技能集合。
func normalizeQuery(q Query) ([]uint16, error) {
	if len(q.Skills) == 0 {
		return nil, fmt.Errorf("%w: required skills must not be empty", ErrInvalidQuery)
	}
	if q.Hours == 0 {
		return nil, fmt.Errorf("%w: hours must be positive", ErrInvalidQuery)
	}
	if q.BudgetMin == 0 || q.BudgetMax == 0 {
		return nil, fmt.Errorf("%w: budget bounds must be positive", ErrInvalidQuery)
	}
	if q.BudgetMax < q.BudgetMin {
		return nil, fmt.Errorf("%w: budget max %d below min %d", ErrInvalidQuery, q.BudgetMax, q.BudgetMin)
	}
	w := q.Weights
	if w.Skill+w.Price+w.Rating+w.Status != 100 {
		return nil, fmt.Errorf("%w: weights must sum to 100, got %d", ErrInvalidQuery, w.Skill+w.Price+w.Rating+w.Status)
	}

	required := make([]uint16, len(q.Skills))
	copy(required, q.Skills)
	sort.Slice(required, func(a, b int) bool { return required[a] < required[b] })
	uniq := required[:0]
	for i, sk := range required {
		if i == 0 || sk != required[i-1] {
			uniq = append(uniq, sk)
		}
	}
	return uniq, nil
}
