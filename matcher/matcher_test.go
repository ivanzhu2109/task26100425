package matcher

import (
	"math/rand"
	"sort"
	"testing"
)

// referenceMatch 为独立的逐人暴力实现：全量评分、全量排序，用于对照。
func referenceMatch(candidates []Candidate, q Query) []Match {
	required := map[uint16]struct{}{}
	for _, sk := range q.Skills {
		required[sk] = struct{}{}
	}
	type scored struct {
		id    int32
		score uint64
	}
	var all []scored
	for _, c := range candidates {
		seen := map[uint16]struct{}{}
		matched := 0
		for _, sk := range c.Skills {
			if _, dup := seen[sk]; dup {
				continue
			}
			seen[sk] = struct{}{}
			if _, ok := required[sk]; ok {
				matched++
			}
		}
		skill := uint64(matched) * 10000 / uint64(len(required))

		cost := uint64(c.HourlyRate) * uint64(q.Hours)
		var price uint64
		switch {
		case cost < q.BudgetMin:
			price = cost * 10000 / q.BudgetMin
		case cost > q.BudgetMax:
			price = q.BudgetMax * 10000 / cost
		default:
			price = 10000
		}

		var rating uint64
		if c.RatingCount > 0 {
			rating = uint64(c.RatingSum) * 10000 / (uint64(c.RatingCount) * 5)
		}

		var status uint64
		switch c.Status {
		case StatusAvailable:
			status = 10000
		case StatusBusy:
			status = 5000
		}

		score := skill*uint64(q.Weights.Skill) + price*uint64(q.Weights.Price) +
			rating*uint64(q.Weights.Rating) + status*uint64(q.Weights.Status)
		if score >= q.MinScore {
			all = append(all, scored{c.ID, score})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].id < all[j].id
	})
	if len(all) > TopN {
		all = all[:TopN]
	}
	out := make([]Match, len(all))
	for i, s := range all {
		out[i] = Match{CandidateID: s.id, Score: s.score}
	}
	return out
}

func loadStore(t *testing.T, candidates []Candidate) *Store {
	t.Helper()
	s := &Store{}
	if err := s.Load(candidates); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func assertMatches(t *testing.T, got, want []Match) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d matches %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("match[%d] = %+v, want %+v (got %v, want %v)", i, got[i], want[i], got, want)
		}
	}
}

func TestZeroSkillOverlapCanMakeTop20(t *testing.T) {
	var candidates []Candidate
	// 19 个技能全中但口碑为 0、休假且报价超出预算上限的候选人。
	for i := 0; i < 19; i++ {
		candidates = append(candidates, Candidate{
			ID:         int32(1000 + i),
			Skills:     []uint16{1, 2, 3},
			HourlyRate: 9000,
			Status:     StatusOnLeave,
		})
	}
	// 一个零技能重合、但可接单且口碑满分、价格落在预算内的候选人。
	star := Candidate{
		ID:          7,
		Skills:      []uint16{900, 901},
		HourlyRate:  5000,
		Status:      StatusAvailable,
		RatingSum:   250,
		RatingCount: 50,
	}
	candidates = append(candidates, star)

	s := loadStore(t, candidates)
	q := Query{
		Skills:    []uint16{1, 2, 3},
		Hours:     10,
		BudgetMin: 40000,
		BudgetMax: 60000,
		Weights:   DefaultWeights(),
	}
	got, err := s.Match(q)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	want := referenceMatch(candidates, q)
	assertMatches(t, got, want)
	if len(got) != 20 {
		t.Fatalf("expected 20 results, got %d", len(got))
	}
	if got[0].CandidateID != 7 {
		t.Fatalf("zero-overlap candidate should rank first, got %+v", got[0])
	}
}

func TestWeightChangeFlipsRanking(t *testing.T) {
	cheap := Candidate{ID: 1, Skills: nil, HourlyRate: 3000, Status: StatusOnLeave}
	skilled := Candidate{ID: 2, Skills: []uint16{1, 2, 3, 4, 5}, HourlyRate: 19000, Status: StatusAvailable}
	candidates := []Candidate{cheap, skilled}
	s := loadStore(t, candidates)

	base := Query{Skills: []uint16{1, 2, 3, 4, 5}, Hours: 10, BudgetMin: 20000, BudgetMax: 100000}

	skillFirst, err := s.Match(Query{
		Skills: base.Skills, Hours: base.Hours, BudgetMin: base.BudgetMin, BudgetMax: base.BudgetMax,
		Weights: Weights{Skill: 100},
	})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if skillFirst[0].CandidateID != 2 {
		t.Fatalf("skill weight 100: want candidate 2 first, got %v", skillFirst)
	}

	priceFirst, err := s.Match(Query{
		Skills: base.Skills, Hours: base.Hours, BudgetMin: base.BudgetMin, BudgetMax: base.BudgetMax,
		Weights: Weights{Price: 100},
	})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if priceFirst[0].CandidateID != 1 {
		t.Fatalf("price weight 100: want candidate 1 first, got %v", priceFirst)
	}

	want := referenceMatch(candidates, Query{
		Skills: base.Skills, Hours: base.Hours, BudgetMin: base.BudgetMin, BudgetMax: base.BudgetMax,
		Weights: Weights{Price: 100},
	})
	assertMatches(t, priceFirst, want)
}

func TestTieAroundRank20ByIDAscending(t *testing.T) {
	// 25 个完全同分的候选人（技能分取整相同、其余全同），前 20 名应按编号升序。
	var candidates []Candidate
	ids := rand.New(rand.NewSource(1)).Perm(25)
	for _, id := range ids {
		candidates = append(candidates, Candidate{
			ID:         int32(100 + id),
			Skills:     []uint16{1}, // 3 个所需技能中命中 1 个，技能分 3333
			HourlyRate: 5000,
			Status:     StatusBusy,
		})
	}
	s := loadStore(t, candidates)
	q := Query{
		Skills:    []uint16{1, 2, 3},
		Hours:     4,
		BudgetMin: 10000,
		BudgetMax: 40000,
		Weights:   DefaultWeights(),
	}
	got, err := s.Match(q)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	want := referenceMatch(candidates, q)
	assertMatches(t, got, want)
	if len(got) != 20 {
		t.Fatalf("expected 20 results, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score != got[0].Score {
			t.Fatalf("expected all ties, got[%d]=%+v first=%+v", i, got[i], got[0])
		}
		if got[i].CandidateID <= got[i-1].CandidateID {
			t.Fatalf("ties must be ordered by id ascending: %v", got)
		}
	}
	if got[0].CandidateID != 100 || got[19].CandidateID != 119 {
		t.Fatalf("want ids 100..119, got first=%d last=%d", got[0].CandidateID, got[19].CandidateID)
	}
}

func TestMinScoreExactEqualityIncluded(t *testing.T) {
	c := Candidate{ID: 1, Skills: []uint16{1}, HourlyRate: 5000, Status: StatusAvailable}
	s := loadStore(t, []Candidate{c})
	q := Query{
		Skills:    []uint16{1},
		Hours:     2,
		BudgetMin: 5000,
		BudgetMax: 20000,
		Weights:   DefaultWeights(),
	}
	probe, err := s.Match(q)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(probe) != 1 {
		t.Fatalf("expected 1 result, got %v", probe)
	}
	exact := probe[0].Score

	q.MinScore = exact
	got, err := s.Match(q)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got) != 1 || got[0].Score != exact {
		t.Fatalf("min score equal to score must be included, got %v", got)
	}

	q.MinScore = exact + 1
	got, err = s.Match(q)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("min score above score must be excluded, got %v", got)
	}
}
