package talentmatch

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNoData 表示尚未加载资料快照。
var ErrNoData = errors.New("talentmatch: no snapshot loaded")

// topK 是每次查询返回的最大结果数。
const topK = 20

// Matcher 持有候选人资料快照，提供查询能力。查询期间资料不变。
//
// 内部按列式（SoA）布局存储：与查询无关的口碑分、状态分打包进
// 单个 uint32（高 16 位口碑分、低 16 位状态分）；技能建倒排索引
// （技能 -> 候选人下标），查询时按所需技能累加命中计数，
// 主循环为一次顺序扫描。Matcher 不是并发安全的；
// 加载完成后可并发执行 Query（Query 不修改共享状态，
// 每查询暂存区取自 sync.Pool）。
type Matcher struct {
	ids     []int64  // 候选人编号
	rates   []int64  // 时薪（美分）
	packed  []uint32 // 高 16 位：预计算口碑分；低 16 位：预计算状态分
	univ    []int32  // 技能全集，升序
	post    []int32  // 倒排表：按密集技能编号拼接的候选人下标
	postOff []int32  // 倒排表偏移，len = len(univ)+1
	loaded  bool

	scratchPool sync.Pool // 每查询暂存区 *queryScratch
}

// queryScratch 是单次查询的暂存区，用代际标记避免每次清零。
type queryScratch struct {
	gen []uint32 // 候选人最近被触及的查询代际
	cnt []uint16 // 该代际下命中的所需技能数
	cur uint32   // 当前代际
}

// NewMatcher 返回一个尚未加载资料的空 Matcher。
func NewMatcher() *Matcher {
	return &Matcher{}
}

// Load 加载静态资料快照，替换此前已加载的数据。
// 候选人编号必须唯一，技能编号会被去重（不计重复）。
func (m *Matcher) Load(candidates []Candidate) error {
	n := len(candidates)
	ids := make([]int64, n)
	rates := make([]int64, n)
	packed := make([]uint32, n)

	seen := make(map[int64]struct{}, n)
	dense := make(map[int32]int32, 1024) // 技能编号 -> 密集下标
	denseSkills := make([]int32, 0, n*8) // 每位候选人的去重技能（密集下标），扁平拼接
	denseOff := make([]int32, n+1)

	for i := range candidates {
		c := &candidates[i]
		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("talentmatch: duplicate candidate id %d", c.ID)
		}
		seen[c.ID] = struct{}{}
		if len(c.Skills) > 20 {
			return fmt.Errorf("talentmatch: candidate %d has %d skills, max 20", c.ID, len(c.Skills))
		}
		if c.HourlyRateCents <= 0 {
			return fmt.Errorf("talentmatch: candidate %d has non-positive hourly rate", c.ID)
		}
		if c.Status < StatusAvailable || c.Status > StatusVacation {
			return fmt.Errorf("talentmatch: candidate %d has invalid status %d", c.ID, c.Status)
		}
		if c.RatingCount < 0 {
			return fmt.Errorf("talentmatch: candidate %d has negative rating count", c.ID)
		}
		if c.RatingSum < 0 || c.RatingSum > 5*int64(c.RatingCount) {
			return fmt.Errorf("talentmatch: candidate %d has rating sum %d inconsistent with count %d", c.ID, c.RatingSum, c.RatingCount)
		}

		ids[i] = c.ID
		rates[i] = c.HourlyRateCents
		var rep, status int64
		if c.RatingCount > 0 {
			rep = c.RatingSum * subScoreMax / (int64(c.RatingCount) * 5)
		}
		switch c.Status {
		case StatusAvailable:
			status = subScoreMax
		case StatusBusy:
			status = statusBusy
		}
		packed[i] = uint32(rep)<<16 | uint32(status)

		for _, s := range dedupeSorted(c.Skills) {
			d, ok := dense[s]
			if !ok {
				d = int32(len(dense))
				dense[s] = d
			}
			denseSkills = append(denseSkills, d)
		}
		denseOff[i+1] = int32(len(denseSkills))
	}

	// 技能全集（升序）与密集下标重映射：让 univ 有序，便于查询时二分。
	univ := make([]int32, len(dense))
	for s, d := range dense {
		univ[d] = s
	}
	sort.Slice(univ, func(a, b int) bool { return univ[a] < univ[b] })
	remap := make([]int32, len(dense))
	for newD, s := range univ {
		remap[dense[s]] = int32(newD)
	}

	// 倒排表：计数 + 前缀和 + 填充。
	postOff := make([]int32, len(univ)+1)
	for _, d := range denseSkills {
		postOff[remap[d]+1]++
	}
	for i := 1; i < len(postOff); i++ {
		postOff[i] += postOff[i-1]
	}
	post := make([]int32, len(denseSkills))
	fill := make([]int32, len(univ))
	copy(fill, postOff[:len(univ)])
	for i := 0; i < n; i++ {
		for _, d := range denseSkills[denseOff[i]:denseOff[i+1]] {
			rd := remap[d]
			post[fill[rd]] = int32(i)
			fill[rd]++
		}
	}

	m.ids, m.rates, m.packed = ids, rates, packed
	m.univ, m.post, m.postOff = univ, post, postOff
	m.loaded = true
	return nil
}

// dedupeSorted 返回去重并升序排序的技能编号。
func dedupeSorted(src []int32) []int32 {
	if len(src) == 0 {
		return nil
	}
	s := make([]int32, len(src))
	copy(s, src)
	sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// validate 检查查询参数合法性；权重全零时视为使用默认权重。
func (q *Query) validate() error {
	if len(q.RequiredSkills) == 0 {
		return errors.New("talentmatch: query requires at least one skill")
	}
	if q.EstimatedHours <= 0 {
		return errors.New("talentmatch: estimated hours must be positive")
	}
	if q.BudgetMinCents <= 0 || q.BudgetMaxCents <= 0 {
		return errors.New("talentmatch: budget bounds must be positive")
	}
	if q.BudgetMinCents > q.BudgetMaxCents {
		return errors.New("talentmatch: budget min exceeds max")
	}
	var sum uint32
	for _, w := range q.Weights {
		sum += w
	}
	if sum != 0 && sum != 100 {
		return fmt.Errorf("talentmatch: weights must sum to 100, got %d", sum)
	}
	if sum == 0 {
		q.Weights = DefaultWeights
	}
	return nil
}

// better 定义结果排序：最终分降序，同分按候选人编号升序。
func better(a, b Match) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.CandidateID < b.CandidateID
}

// Query 返回最终分不低于 MinScore 的前 20 名候选人，
// 按最终分降序、同分按候选人编号升序；不足 20 人则全部返回。
//
// 结果与逐人完整评分严格一致：仅使用数学上安全的剪枝
// （上界低于阈值者不可能进入结果），不会因未命中技能而漏人。
func (m *Matcher) Query(q *Query) ([]Match, error) {
	if !m.loaded {
		return nil, ErrNoData
	}
	if err := q.validate(); err != nil {
		return nil, err
	}

	req := dedupeSorted(q.RequiredSkills)
	reqCount := int64(len(req))

	wSkill := int64(q.Weights[WeightSkill])
	wPrice := int64(q.Weights[WeightPrice])
	wRep := int64(q.Weights[WeightReputation])
	wStatus := int64(q.Weights[WeightStatus])
	minScore := q.MinScore
	hours := q.EstimatedHours
	minB := q.BudgetMinCents
	maxB := q.BudgetMaxCents

	n := len(m.ids)

	// 每查询暂存区：倒排累加所需技能命中数。
	sc := m.getScratch(n)
	defer m.putScratch(sc)
	sc.cur++
	if sc.cur == 0 { // 代际回绕，清零重来
		clear(sc.gen)
		sc.cur = 1
	}
	if wSkill > 0 {
		for _, s := range req {
			d := sort.Search(len(m.univ), func(i int) bool { return m.univ[i] >= s })
			if d >= len(m.univ) || m.univ[d] != s {
				continue // 资料中不存在该技能
			}
			for _, ci := range m.post[m.postOff[d]:m.postOff[d+1]] {
				if sc.gen[ci] != sc.cur {
					sc.gen[ci] = sc.cur
					sc.cnt[ci] = 1
				} else {
					sc.cnt[ci]++
				}
			}
		}
	}
	// 命中数 -> 技能分 查表，避免主循环内除法。
	skillTab := make([]int64, len(req)+1)
	for k := range skillTab {
		skillTab[k] = int64(k) * subScoreMax / reqCount
	}

	// 时薪落在 [loRate, hiRate] 内 <=> 预计金额落在预算闭区间内。
	loRate := (minB-1)/hours + 1 // ceil(minB/hours)
	hiRate := maxB / hours       // floor(maxB/hours)

	// 静态上界中技能分与价格分的满分部分。
	staticBound := wSkill*subScoreMax + wPrice*subScoreMax
	cutoff := minScore

	ids := m.ids
	rates := m.rates
	packed := m.packed
	gen := sc.gen
	cnt := sc.cnt
	cur := sc.cur

	top := make([]Match, 0, topK)
	var worst Match // top 满员时的第 20 名
	full := false

	for i := 0; i < n; i++ {
		p := packed[i]
		repW := int64(p>>16) * wRep
		stW := int64(p&0xFFFF) * wStatus
		// 上界剪枝：技能、价格均取满分仍达不到阈值则不可能入选。
		if repW+stW+staticBound < cutoff {
			continue
		}

		var skill int64
		if wSkill > 0 && gen[i] == cur {
			skill = skillTab[cnt[i]]
		}

		var price int64
		if wPrice > 0 {
			rate := rates[i]
			if rate >= loRate && rate <= hiRate {
				price = subScoreMax
			} else {
				amount := rate * hours
				if amount < minB {
					price = amount * subScoreMax / minB
				} else {
					price = maxB * subScoreMax / amount
				}
			}
		}

		final := skill*wSkill + price*wPrice + repW + stW
		if final < cutoff {
			continue
		}

		cand := Match{CandidateID: ids[i], Score: final}
		if !full {
			top = insertSorted(top, cand)
			if len(top) == topK {
				full = true
				worst = top[topK-1]
				if worst.Score > cutoff {
					cutoff = worst.Score
				}
			}
		} else if better(cand, worst) {
			top = insertSorted(top, cand)
			top = top[:topK]
			worst = top[topK-1]
			if worst.Score > cutoff {
				cutoff = worst.Score
			}
		}
	}
	return top, nil
}

func (m *Matcher) getScratch(n int) *queryScratch {
	if v := m.scratchPool.Get(); v != nil {
		sc := v.(*queryScratch)
		if len(sc.gen) >= n {
			return sc
		}
	}
	return &queryScratch{gen: make([]uint32, n), cnt: make([]uint16, n)}
}

func (m *Matcher) putScratch(sc *queryScratch) {
	m.scratchPool.Put(sc)
}

// insertSorted 将 cand 按 better 定义的顺序插入已排序切片。
func insertSorted(top []Match, cand Match) []Match {
	pos := sort.Search(len(top), func(i int) bool { return better(cand, top[i]) })
	top = append(top, Match{})
	copy(top[pos+1:], top[pos:])
	top[pos] = cand
	return top
}
