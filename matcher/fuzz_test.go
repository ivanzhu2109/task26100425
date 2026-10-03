package matcher

import (
	"math/rand"
	"testing"
)

// randomStore 生成 rnd 流确定的随机候选人集合。
func randomStore(rnd *rand.Rand, n, skillSpace int) []Candidate {
	candidates := make([]Candidate, n)
	for i := range candidates {
		skills := make([]uint16, rnd.Intn(21))
		for j := range skills {
			skills[j] = uint16(rnd.Intn(skillSpace))
		}
		var ratingSum uint32
		var ratingCount uint16
		if rnd.Intn(4) > 0 { // 约四分之一没有评价
			ratingCount = uint16(rnd.Intn(51))
			for k := 0; k < int(ratingCount); k++ {
				ratingSum += uint32(1 + rnd.Intn(5))
			}
		}
		candidates[i] = Candidate{
			ID:          int32(rnd.Intn(1 << 30)),
			Skills:      skills,
			HourlyRate:  uint32(1 + rnd.Intn(200000)),
			Status:      Status(rnd.Intn(3)),
			RatingSum:   ratingSum,
			RatingCount: ratingCount,
		}
	}
	// 偶发重复编号由调用方去重，保证快照合法。
	seen := map[int32]struct{}{}
	out := candidates[:0]
	for _, c := range candidates {
		if _, dup := seen[c.ID]; dup {
			continue
		}
		seen[c.ID] = struct{}{}
		out = append(out, c)
	}
	return out
}

// randomWeights 生成合计 100 的随机权重，覆盖单项 100 与含 0 的情况。
func randomWeights(rnd *rand.Rand) Weights {
	switch rnd.Intn(6) {
	case 0:
		return DefaultWeights()
	case 1:
		return Weights{Skill: 100}
	case 2:
		return Weights{Price: 100}
	case 3:
		return Weights{Rating: 100}
	case 4:
		return Weights{Status: 100}
	default:
		a, b, c := rnd.Intn(101), 0, 0
		b = rnd.Intn(101 - a)
		c = rnd.Intn(101 - a - b)
		return Weights{Skill: uint32(a), Price: uint32(b), Rating: uint32(c), Status: uint32(100 - a - b - c)}
	}
}

// randomQuery 生成随机查询，预算上下限均为正数且上限不小于下限。
func randomQuery(rnd *rand.Rand, skillSpace int) Query {
	skills := make([]uint16, 1+rnd.Intn(8))
	for i := range skills {
		skills[i] = uint16(rnd.Intn(skillSpace))
	}
	lo := uint64(1 + rnd.Intn(2000000))
	hi := lo + uint64(rnd.Intn(2000000))
	var minScore uint64
	switch rnd.Intn(3) {
	case 1:
		minScore = uint64(rnd.Intn(1000001))
	case 2:
		minScore = uint64(rnd.Intn(400000)) // 低最低分，保证结果常被截断到 20
	}
	return Query{
		Skills:    skills,
		Hours:     uint32(1 + rnd.Intn(200)),
		BudgetMin: lo,
		BudgetMax: hi,
		Weights:   randomWeights(rnd),
		MinScore:  minScore,
	}
}

// TestFuzzAgainstReference 随机快照与随机查询全量对照独立暴力实现。
func TestFuzzAgainstReference(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	for round := 0; round < 60; round++ {
		n := 1 + rnd.Intn(300)
		if round%10 == 0 {
			n = 2000 // 周期性使用较大快照，让结果频繁被截断到 20
		}
		skillSpace := 5 + rnd.Intn(200)
		candidates := randomStore(rnd, n, skillSpace)
		s := loadStore(t, candidates)
		for qi := 0; qi < 25; qi++ {
			q := randomQuery(rnd, skillSpace)
			got, err := s.Match(q)
			if err != nil {
				t.Fatalf("round %d query %d: %v", round, qi, err)
			}
			want := referenceMatch(candidates, q)
			assertMatches(t, got, want)
		}
	}
}

// TestLoadValidation 校验非法快照被拒绝。
func TestLoadValidation(t *testing.T) {
	ok := Candidate{ID: 1, HourlyRate: 100, Status: StatusBusy}
	cases := map[string][]Candidate{
		"zero rate":       {{ID: 1, HourlyRate: 0}},
		"bad status":      {{ID: 1, HourlyRate: 1, Status: Status(9)}},
		"too many skills": {{ID: 1, HourlyRate: 1, Skills: make([]uint16, 21)}},
		"duplicate id":    {ok, ok},
	}
	for name, candidates := range cases {
		s := &Store{}
		if err := s.Load(candidates); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
	if err := (&Store{}).Load([]Candidate{ok}); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
}

// TestQueryValidation 校验非法查询被拒绝。
func TestQueryValidation(t *testing.T) {
	s := loadStore(t, []Candidate{{ID: 1, HourlyRate: 100}})
	base := Query{
		Skills: []uint16{1}, Hours: 1, BudgetMin: 1, BudgetMax: 2,
		Weights: DefaultWeights(),
	}
	bad := map[string]Query{
		"no skills":       {Hours: 1, BudgetMin: 1, BudgetMax: 2, Weights: DefaultWeights()},
		"zero hours":      {Skills: []uint16{1}, BudgetMin: 1, BudgetMax: 2, Weights: DefaultWeights()},
		"zero min":        {Skills: []uint16{1}, Hours: 1, BudgetMax: 2, Weights: DefaultWeights()},
		"zero max":        {Skills: []uint16{1}, Hours: 1, BudgetMin: 1, Weights: DefaultWeights()},
		"max below min":   {Skills: []uint16{1}, Hours: 1, BudgetMin: 5, BudgetMax: 4, Weights: DefaultWeights()},
		"weights not 100": {Skills: []uint16{1}, Hours: 1, BudgetMin: 1, BudgetMax: 2, Weights: Weights{Skill: 50}},
	}
	for name, q := range bad {
		if _, err := s.Match(q); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
	if _, err := s.Match(base); err != nil {
		t.Fatalf("valid query rejected: %v", err)
	}
}

// TestWideRateRangeFallback 时薪跨度超过查表上限时走逐人除法回退路径，
// 结果仍须与独立暴力实现一致。
func TestWideRateRangeFallback(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	candidates := make([]Candidate, 500)
	for i := range candidates {
		ratingCount := uint16(rnd.Intn(51))
		candidates[i] = Candidate{
			ID:          int32(i + 1),
			Skills:      []uint16{uint16(rnd.Intn(50)), uint16(rnd.Intn(50))},
			HourlyRate:  uint32(1 + rnd.Int63n(1<<32-1)),
			Status:      Status(rnd.Intn(3)),
			RatingSum:   uint32(rnd.Intn(int(ratingCount)*5 + 1)),
			RatingCount: ratingCount,
		}
	}
	s := loadStore(t, candidates)
	for qi := 0; qi < 20; qi++ {
		q := randomQuery(rnd, 50)
		q.BudgetMin = uint64(1 + rnd.Int63n(1<<40))
		q.BudgetMax = q.BudgetMin + uint64(rnd.Int63n(1<<40))
		got, err := s.Match(q)
		if err != nil {
			t.Fatalf("query %d: %v", qi, err)
		}
		assertMatches(t, got, referenceMatch(candidates, q))
	}
}
