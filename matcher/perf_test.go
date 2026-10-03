package matcher

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"
)

// 性能验收规模常量。
const (
	perfCandidates = 500000
	perfSkillSpace = 10000
	perfQueries    = 2000
	perfRateMin    = 1000  // 10 美元/小时，美分
	perfRateMax    = 20000 // 200 美元/小时，美分
)

// perfSnapshot 按验收分布生成快照：每人 10 种技能、技能均匀分散、
// 时薪 10~200 美元、三种状态各约三分之一、口碑覆盖完整星级范围。
func perfSnapshot(rnd *rand.Rand) []Candidate {
	candidates := make([]Candidate, perfCandidates)
	for i := range candidates {
		skills := make([]uint16, 10)
		for j := range skills {
			skills[j] = uint16(rnd.Intn(perfSkillSpace))
		}
		ratingCount := uint16(rnd.Intn(51)) // 含 0 条评价
		var ratingSum uint32
		for k := 0; k < int(ratingCount); k++ {
			ratingSum += uint32(1 + rnd.Intn(5))
		}
		candidates[i] = Candidate{
			ID:          int32(i + 1),
			Skills:      skills,
			HourlyRate:  uint32(perfRateMin + rnd.Intn(perfRateMax-perfRateMin+1)),
			Status:      Status(rnd.Intn(3)),
			RatingSum:   ratingSum,
			RatingCount: ratingCount,
		}
	}
	return candidates
}

// perfQuery 按验收分布生成查询：5 种所需技能、预算折合时薪落在 10~200 美元、
// 预算宽度为该区间宽度的 5%~50%；四分之一查询技能权重为 0，含单项权重 100。
func perfQuery(rnd *rand.Rand, i int) Query {
	skills := make([]uint16, 5)
	for j := range skills {
		skills[j] = uint16(rnd.Intn(perfSkillSpace))
	}
	hours := uint32(1 + rnd.Intn(160))
	center := perfRateMin + rnd.Intn(perfRateMax-perfRateMin+1)
	width := (perfRateMax - perfRateMin) * (5 + rnd.Intn(46)) / 100 // 5%~50%
	lo := center - width/2
	if lo < 1 {
		lo = 1
	}
	hi := center + width/2

	var weights Weights
	switch {
	case i%4 == 3: // 四分之一查询技能权重为 0
		rest := rnd.Intn(101)
		weights = Weights{Skill: 0, Price: uint32(rest), Rating: uint32(rnd.Intn(101 - rest))}
		weights.Status = 100 - weights.Price - weights.Rating
	case i%7 == 6: // 单项权重 100
		switch rnd.Intn(4) {
		case 0:
			weights = Weights{Skill: 100}
		case 1:
			weights = Weights{Price: 100}
		case 2:
			weights = Weights{Rating: 100}
		default:
			weights = Weights{Status: 100}
		}
	case i%2 == 0:
		weights = DefaultWeights()
	default:
		a := rnd.Intn(101)
		b := rnd.Intn(101 - a)
		c := rnd.Intn(101 - a - b)
		weights = Weights{Skill: uint32(a), Price: uint32(b), Rating: uint32(c), Status: uint32(100 - a - b - c)}
	}

	var minScore uint64
	switch rnd.Intn(4) {
	case 1:
		minScore = uint64(rnd.Intn(1000001))
	case 2:
		minScore = uint64(rnd.Intn(600000))
	}

	return Query{
		Skills:    skills,
		Hours:     hours,
		BudgetMin: uint64(lo) * uint64(hours),
		BudgetMax: uint64(hi) * uint64(hours),
		Weights:   weights,
		MinScore:  minScore,
	}
}

// TestFullScalePerformance 全规模性能验收：加载 50 万候选人，连续处理 2000 个查询，
// 报告加载与查询 CPU 耗时及峰值内存。单线程运行，墙钟时间即 CPU 耗时。
func TestFullScalePerformance(t *testing.T) {
	runtime.GOMAXPROCS(1)
	rnd := rand.New(rand.NewSource(20261003))

	snapshot := perfSnapshot(rnd)
	queries := make([]Query, perfQueries)
	for i := range queries {
		queries[i] = perfQuery(rnd, i)
	}

	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	loadStart := time.Now()
	s := &Store{}
	if err := s.Load(snapshot); err != nil {
		t.Fatalf("Load: %v", err)
	}
	loadElapsed := time.Since(loadStart)

	var memAfterLoad runtime.MemStats
	runtime.ReadMemStats(&memAfterLoad)

	var totalMatches int
	queryStart := time.Now()
	for _, q := range queries {
		matches, err := s.Match(q)
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		totalMatches += len(matches)
	}
	queryElapsed := time.Since(queryStart)

	var memAfterQueries runtime.MemStats
	runtime.ReadMemStats(&memAfterQueries)

	fmt.Printf("加载: %d 名候选人, CPU 耗时 %v (上限 45s)\n", s.Len(), loadElapsed)
	fmt.Printf("查询: %d 个查询, 累计 CPU 耗时 %v (上限 10s), 平均 %v/查询, 返回结果总数 %d\n",
		perfQueries, queryElapsed, queryElapsed/perfQueries, totalMatches)
	fmt.Printf("内存: 加载后堆占用 %.1f MiB, 查询后堆占用 %.1f MiB, 向系统申请总量 %.1f MiB (上限 512 MiB)\n",
		float64(memAfterLoad.HeapAlloc)/1048576, float64(memAfterQueries.HeapAlloc)/1048576,
		float64(memAfterQueries.Sys)/1048576)

	if loadElapsed > 45*time.Second {
		t.Fatalf("加载超时: %v > 45s", loadElapsed)
	}
	if queryElapsed > 10*time.Second {
		t.Fatalf("查询超时: %v > 10s", queryElapsed)
	}
	if memAfterQueries.Sys > 512<<20 {
		t.Fatalf("内存超限: %d > 512 MiB", memAfterQueries.Sys)
	}
}
