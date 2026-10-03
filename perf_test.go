package talentmatch

import (
	"math/rand"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// cpuTime 返回进程用户态+内核态 CPU 耗时。
func cpuTime() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond +
		time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
}

// memSampler 周期性采样堆内存，返回峰值（字节）。
func memSampler(stop <-chan struct{}, peak *uint64) {
	var ms runtime.MemStats
	for {
		select {
		case <-stop:
			return
		default:
			runtime.ReadMemStats(&ms)
			inuse := ms.HeapInuse + ms.StackInuse
			if inuse > *peak {
				*peak = inuse
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// TestPerfFullScale 全规模性能验收：500000 候选人、10000 种技能、2000 次查询。
// 运行：go test -run TestPerfFullScale -v -timeout 30m
func TestPerfFullScale(t *testing.T) {
	const (
		nCandidates = 500000
		nSkills     = 10000
		nQueries    = 2000
	)
	r := rand.New(rand.NewSource(20261003))

	stop := make(chan struct{})
	var peak uint64
	go memSampler(stop, &peak)
	defer close(stop)

	// --- 生成资料：每人 10 技能均匀分散，时薪 $10..$200，状态各约 1/3，口碑覆盖完整星级 ---
	genStart := time.Now()
	cands := make([]Candidate, nCandidates)
	for i := range cands {
		skills := make([]int32, 10)
		for j := range skills {
			skills[j] = int32(r.Intn(nSkills))
		}
		cnt := int32(r.Intn(51)) // 0..50 条
		var sum int64
		if cnt > 0 {
			sum = int64(r.Intn(int(5*cnt) + 1)) // 覆盖完整星级范围
		}
		cands[i] = Candidate{
			ID:              int64(i + 1),
			Skills:          skills,
			HourlyRateCents: int64(1000 + r.Intn(19001)),
			Status:          Status(r.Intn(3)),
			RatingSum:       sum,
			RatingCount:     cnt,
		}
	}
	genElapsed := time.Since(genStart)

	// --- 加载 ---
	m := NewMatcher()
	cpuStart := cpuTime()
	wallStart := time.Now()
	if err := m.Load(cands); err != nil {
		t.Fatal(err)
	}
	loadCPU := cpuTime() - cpuStart
	loadWall := time.Since(wallStart)

	// --- 生成 2000 个查询：默认/覆盖权重、不同最低分，1/4 技能权重为 0，含单项 100 ---
	queries := make([]Query, nQueries)
	for i := range queries {
		skills := make([]int32, 5)
		for j := range skills {
			skills[j] = int32(r.Intn(nSkills))
		}
		hours := int64(1 + r.Intn(200))
		hourly := int64(1000 + r.Intn(19001))    // 折合时薪 $10..$200
		width := hours * int64(950+r.Intn(8551)) // 预算宽度为区间宽度(19000/h)的 5%..50%
		q := Query{
			RequiredSkills: skills,
			EstimatedHours: hours,
			BudgetMinCents: hourly * hours,
			BudgetMaxCents: hourly*hours + width,
			MinScore:       int64(r.Intn(1000001)),
		}
		switch i % 8 {
		case 0, 1: // 默认权重
			q.Weights = DefaultWeights
		case 2, 3: // 技能权重为 0（占 1/4）
			q.Weights = [4]uint32{0, 40, 35, 25}
		case 4: // 单项权重 100
			q.Weights = [4]uint32{100, 0, 0, 0}
		case 5:
			q.Weights = [4]uint32{0, 100, 0, 0}
		case 6: // 随机覆盖权重
			for sum := uint32(0); sum != 100; {
				q.Weights = [4]uint32{uint32(r.Intn(101)), uint32(r.Intn(101)), uint32(r.Intn(101)), uint32(r.Intn(101))}
				sum = q.Weights[0] + q.Weights[1] + q.Weights[2] + q.Weights[3]
			}
		case 7:
			q.Weights = [4]uint32{10, 30, 30, 30}
		}
		queries[i] = q
	}

	// --- 查询 ---
	cpuStart = cpuTime()
	wallStart = time.Now()
	total := 0
	for i := range queries {
		res, err := m.Query(&queries[i])
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		total += len(res)
	}
	queryCPU := cpuTime() - cpuStart
	queryWall := time.Since(wallStart)

	const mib = 1024 * 1024
	t.Logf("数据生成(不计入验收): %v", genElapsed)
	t.Logf("加载: CPU %v, 墙钟 %v (预算 45s)", loadCPU, loadWall)
	t.Logf("查询: 2000 次累计 CPU %v, 墙钟 %v (预算 10s), 平均 %v/次", queryCPU, queryWall, queryCPU/nQueries)
	t.Logf("峰值内存(堆+栈, 含资料与查询辅助信息): %.1f MiB", float64(peak)/mib)
	t.Logf("结果总数: %d", total)

	if loadCPU > 45*time.Second {
		t.Errorf("加载 CPU 耗时超标: %v > 45s", loadCPU)
	}
	if queryCPU > 10*time.Second {
		t.Errorf("查询 CPU 耗时超标: %v > 10s", queryCPU)
	}
	if peak > 512*mib {
		t.Errorf("峰值内存超标: %.1f MiB > 512 MiB", float64(peak)/mib)
	}
}
