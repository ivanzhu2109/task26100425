package talentmatch

import (
	"math/rand"
	"sort"
	"testing"
)

// --- 独立参考实现：逐人完整计算，全量排序取前 20 ---

func refScore(c Candidate, q Query, required map[int32]struct{}) int64 {
	seen := map[int32]bool{}
	var matched int64
	for _, s := range c.Skills {
		if seen[s] {
			continue
		}
		seen[s] = true
		if _, ok := required[s]; ok {
			matched++
		}
	}
	skill := matched * 10000 / int64(len(required))

	amount := c.HourlyRateCents * q.EstimatedHours
	var price int64
	if amount < q.BudgetMinCents {
		price = amount * 10000 / q.BudgetMinCents
	} else if amount > q.BudgetMaxCents {
		price = q.BudgetMaxCents * 10000 / amount
	} else {
		price = 10000
	}

	var rep int64
	if c.RatingCount > 0 {
		rep = c.RatingSum * 10000 / (int64(c.RatingCount) * 5)
	}

	var status int64
	switch c.Status {
	case StatusAvailable:
		status = 10000
	case StatusBusy:
		status = 5000
	}

	w := q.Weights
	return skill*int64(w[0]) + price*int64(w[1]) + rep*int64(w[2]) + status*int64(w[3])
}

func refQuery(cands []Candidate, q Query) []Match {
	required := map[int32]struct{}{}
	for _, s := range q.RequiredSkills {
		required[s] = struct{}{}
	}
	if q.Weights == [4]uint32{} {
		q.Weights = DefaultWeights
	}
	var all []Match
	for _, c := range cands {
		sc := refScore(c, q, required)
		if sc >= q.MinScore {
			all = append(all, Match{CandidateID: c.ID, Score: sc})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].CandidateID < all[j].CandidateID
	})
	if len(all) > 20 {
		all = all[:20]
	}
	return all
}

func assertMatches(t *testing.T, got, want []Match) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("result count: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("rank %d: got %+v, want %+v\ngot:  %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

func randCandidates(r *rand.Rand, n, nSkills int) []Candidate {
	cs := make([]Candidate, n)
	for i := range cs {
		skills := make([]int32, nSkills)
		for j := range skills {
			skills[j] = int32(r.Intn(10000))
		}
		cnt := int32(r.Intn(51))
		var sum int64
		if cnt > 0 {
			sum = int64(r.Intn(int(5*cnt) + 1))
		}
		cs[i] = Candidate{
			ID:              int64(i + 1),
			Skills:          skills,
			HourlyRateCents: int64(1000 + r.Intn(19001)), // $10..$200
			Status:          Status(r.Intn(3)),
			RatingSum:       sum,
			RatingCount:     cnt,
		}
		if r.Intn(10) == 0 {
			cs[i].RatingSum, cs[i].RatingCount = 0, 0
		}
	}
	return cs
}

func randQuery(r *rand.Rand) Query {
	skills := make([]int32, 5)
	for i := range skills {
		skills[i] = int32(r.Intn(10000))
	}
	lo := int64(1000 + r.Intn(19001))
	width := lo/20 + r.Int63n(lo/2) // 5%..50% 宽度
	w := [4]uint32{40, 25, 25, 10}
	switch r.Intn(4) {
	case 0: // 随机权重
		for sum := uint32(0); sum != 100; {
			w = [4]uint32{uint32(r.Intn(101)), uint32(r.Intn(101)), uint32(r.Intn(101)), uint32(r.Intn(101))}
			sum = w[0] + w[1] + w[2] + w[3]
		}
	case 1: // 技能权重为 0
		w = [4]uint32{0, 40, 40, 20}
	case 2: // 单项权重 100
		w = [4]uint32{}
		w[r.Intn(4)] = 100
	}
	return Query{
		RequiredSkills: skills,
		EstimatedHours: int64(1 + r.Intn(200)),
		BudgetMinCents: lo,
		BudgetMaxCents: lo + width,
		Weights:        w,
		MinScore:       int64(r.Intn(1000001)),
	}
}

func TestQueryMatchesReference(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	m := NewMatcher()
	cands := randCandidates(r, 3000, 10)
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		q := randQuery(r)
		got, err := m.Query(&q)
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		assertMatches(t, got, refQuery(cands, q))
	}
}

// 零技能重合者凭价格/口碑/状态分挤入前 20。
func TestZeroSkillOverlapRanks(t *testing.T) {
	m := NewMatcher()
	var cands []Candidate
	// 30 个技能全中但其他维度很差的候选人
	for i := 0; i < 30; i++ {
		cands = append(cands, Candidate{
			ID: int64(100 + i), Skills: []int32{1, 2, 3},
			HourlyRateCents: 20000, Status: StatusVacation,
			RatingSum: 0, RatingCount: 0,
		})
	}
	// 1 个零技能重合但其余维度满分的候选人
	cands = append(cands, Candidate{
		ID: 1, Skills: []int32{999},
		HourlyRateCents: 10000, Status: StatusAvailable,
		RatingSum: 250, RatingCount: 50,
	})
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	q := Query{
		RequiredSkills: []int32{1, 2, 3},
		EstimatedHours: 1,
		BudgetMinCents: 10000, BudgetMaxCents: 10000,
		Weights: [4]uint32{40, 25, 25, 10},
	}
	got, err := m.Query(&q)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, refQuery(cands, q))
	// 零技能重合者得分 = 0*40 + 10000*25 + 10000*25 + 10000*10 = 600000
	// 技能全中者得分 = 10000*40 + 0 + 0 + 0 = 400000
	if got[0].CandidateID != 1 || got[0].Score != 600000 {
		t.Fatalf("zero-overlap candidate should rank first with 600000, got %+v", got[0])
	}
	if len(got) != 20 {
		t.Fatalf("want 20 results, got %d", len(got))
	}
}

// 休假者仍有资格上榜。
func TestVacationEligible(t *testing.T) {
	m := NewMatcher()
	cands := []Candidate{{
		ID: 7, Skills: []int32{1}, HourlyRateCents: 5000,
		Status: StatusVacation, RatingSum: 200, RatingCount: 50,
	}}
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	q := Query{
		RequiredSkills: []int32{1}, EstimatedHours: 1,
		BudgetMinCents: 5000, BudgetMaxCents: 5000,
		Weights: [4]uint32{40, 25, 25, 10},
	}
	got, err := m.Query(&q)
	if err != nil {
		t.Fatal(err)
	}
	// 10000*40 + 10000*25 + 8000*25 + 0*10 = 850000
	want := []Match{{CandidateID: 7, Score: 850000}}
	assertMatches(t, got, want)
}

// 改变权重后榜单大幅翻转。
func TestWeightsFlipRanking(t *testing.T) {
	m := NewMatcher()
	cands := []Candidate{
		{ID: 1, Skills: []int32{1, 2, 3}, HourlyRateCents: 20000, Status: StatusVacation, RatingSum: 0, RatingCount: 0},
		{ID: 2, Skills: []int32{999}, HourlyRateCents: 10000, Status: StatusAvailable, RatingSum: 250, RatingCount: 50},
	}
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	base := Query{
		RequiredSkills: []int32{1, 2, 3}, EstimatedHours: 1,
		BudgetMinCents: 10000, BudgetMaxCents: 10000,
	}
	skillHeavy := base
	skillHeavy.Weights = [4]uint32{100, 0, 0, 0}
	got, err := m.Query(&skillHeavy)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CandidateID != 1 {
		t.Fatalf("skill-heavy: candidate 1 should lead, got %+v", got)
	}
	otherHeavy := base
	otherHeavy.Weights = [4]uint32{0, 40, 40, 20}
	got, err = m.Query(&otherHeavy)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CandidateID != 2 {
		t.Fatalf("other-heavy: candidate 2 should lead, got %+v", got)
	}
}

// 取整造成第 20 名附近多人同分：同分按编号升序，且恰好取满 20 人。
func TestFlooringTiesAroundRank20(t *testing.T) {
	m := NewMatcher()
	var cands []Candidate
	// 25 个完全同分的候选人（技能 1/3 命中，分数相同）
	for i := 0; i < 25; i++ {
		cands = append(cands, Candidate{
			ID:     int64(100 - i), // 编号倒序，验证升序输出
			Skills: []int32{1}, HourlyRateCents: 10000,
			Status: StatusBusy, RatingSum: 0, RatingCount: 0,
		})
	}
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	q := Query{
		RequiredSkills: []int32{1, 2, 3}, EstimatedHours: 1,
		BudgetMinCents: 10000, BudgetMaxCents: 10000,
		Weights: [4]uint32{40, 25, 25, 10},
	}
	got, err := m.Query(&q)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, refQuery(cands, q))
	if len(got) != 20 {
		t.Fatalf("want 20 results, got %d", len(got))
	}
	// 同分 3333*40+10000*25+0+5000*10 = 433320，编号最小的 20 个
	for i, mt := range got {
		wantID := int64(100 - 24 + i)
		if mt.CandidateID != wantID || mt.Score != 433320 {
			t.Fatalf("rank %d: got %+v, want id=%d score=433320", i, mt, wantID)
		}
	}
}

// 最低分恰好等于候选人得分时，该候选人必须入选。
func TestMinScoreInclusive(t *testing.T) {
	m := NewMatcher()
	cands := []Candidate{{
		ID: 5, Skills: []int32{1}, HourlyRateCents: 10000,
		Status: StatusAvailable, RatingSum: 250, RatingCount: 50,
	}}
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	q := Query{
		RequiredSkills: []int32{1}, EstimatedHours: 1,
		BudgetMinCents: 10000, BudgetMaxCents: 10000,
		Weights:  [4]uint32{40, 25, 25, 10},
		MinScore: 1000000, // 恰好等于满分
	}
	got, err := m.Query(&q)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{{CandidateID: 5, Score: 1000000}})

	q.MinScore = 1000001 // 超过满分则无人入选
	got, err = m.Query(&q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestDefaultsAndValidation(t *testing.T) {
	m := NewMatcher()
	if _, err := m.Query(&Query{RequiredSkills: []int32{1}, EstimatedHours: 1, BudgetMinCents: 1, BudgetMaxCents: 1}); err != ErrNoData {
		t.Fatalf("want ErrNoData, got %v", err)
	}
	cands := []Candidate{{ID: 1, Skills: []int32{1, 1, 2}, HourlyRateCents: 100, Status: StatusAvailable}}
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	// 全零权重 -> 默认 40/25/25/10；重复技能去重后 2/2 命中
	q := Query{RequiredSkills: []int32{1, 2}, EstimatedHours: 1, BudgetMinCents: 100, BudgetMaxCents: 100}
	got, err := m.Query(&q)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{{CandidateID: 1, Score: 10000*40 + 10000*25 + 0 + 10000*10}})

	bad := []Query{
		{EstimatedHours: 1, BudgetMinCents: 1, BudgetMaxCents: 1},                             // 空技能
		{RequiredSkills: []int32{1}, EstimatedHours: 0, BudgetMinCents: 1, BudgetMaxCents: 1}, // 工时非正
		{RequiredSkills: []int32{1}, EstimatedHours: 1, BudgetMinCents: 0, BudgetMaxCents: 1}, // 下限非正
		{RequiredSkills: []int32{1}, EstimatedHours: 1, BudgetMinCents: 5, BudgetMaxCents: 4}, // 上下限颠倒
		{RequiredSkills: []int32{1}, EstimatedHours: 1, BudgetMinCents: 1, BudgetMaxCents: 1,
			Weights: [4]uint32{50, 50, 50, 50}}, // 权重和不为 100
	}
	for i, q := range bad {
		if _, err := m.Query(&q); err == nil {
			t.Fatalf("bad query %d: want error", i)
		}
	}

	if err := m.Load([]Candidate{{ID: 1, HourlyRateCents: 1}, {ID: 1, HourlyRateCents: 1}}); err == nil {
		t.Fatal("duplicate ids: want error")
	}
	if err := m.Load([]Candidate{{ID: 1, HourlyRateCents: 1, Skills: make([]int32, 21)}}); err == nil {
		t.Fatal("21 skills: want error")
	}
	if err := m.Load([]Candidate{{ID: 1, HourlyRateCents: 0}}); err == nil {
		t.Fatal("zero rate: want error")
	}
}

// 优化查询路径与 scorer.go 直线实现逐人对比。
func TestFastPathMatchesScorer(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	m := NewMatcher()
	cands := randCandidates(r, 2000, 10)
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		q := randQuery(r)
		got, err := m.Query(&q)
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		required := map[int32]struct{}{}
		for _, s := range q.RequiredSkills {
			required[s] = struct{}{}
		}
		var all []Match
		for j := range cands {
			_, _, _, _, final := scoreCandidate(&cands[j], &q, required)
			if final >= q.MinScore {
				all = append(all, Match{CandidateID: cands[j].ID, Score: final})
			}
		}
		sort.Slice(all, func(a, b int) bool { return better(all[a], all[b]) })
		if len(all) > 20 {
			all = all[:20]
		}
		assertMatches(t, got, all)
	}
}
