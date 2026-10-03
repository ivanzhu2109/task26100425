package matcher

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidCandidate 表示候选人资料不合法。
var ErrInvalidCandidate = errors.New("matcher: invalid candidate")

// maxSkills 为每位候选人允许的最大技能数。
const maxSkills = 20

// staticRec 为查询期间不变的单候选人数据，8 字节对齐紧凑排列。
type staticRec struct {
	hourlyRate  uint32
	ratingScore uint16 // 预计算的口碑分，0 至 10000
	statusScore uint16 // 预计算的状态分：10000、5000、0
}

// Store 保存候选人快照，按结构体数组（SoA）布局以加速全量扫描。
// 加载后资料不变，可并发查询。
type Store struct {
	ids         []int32
	statics     []staticRec
	skillCounts []uint8
	skills      []uint16 // 定长步长 maxSkills，每段升序，仅前 skillCounts[i] 项有效
	minRate     uint32
	maxRate     uint32
}

// Load 校验并加载候选人快照，替换 Store 原有内容。
// 技能编号会去重并按升序保存，重复技能不影响匹配计数。
func (s *Store) Load(candidates []Candidate) error {
	n := len(candidates)
	ids := make([]int32, n)
	statics := make([]staticRec, n)
	skillCounts := make([]uint8, n)
	skills := make([]uint16, n*maxSkills)

	seen := make(map[int32]struct{}, n)
	var minRate, maxRate uint32
	for i, c := range candidates {
		if c.HourlyRate == 0 {
			return fmt.Errorf("%w: candidate %d: hourly rate must be positive", ErrInvalidCandidate, c.ID)
		}
		if c.Status > StatusOnLeave {
			return fmt.Errorf("%w: candidate %d: unknown status %d", ErrInvalidCandidate, c.ID, c.Status)
		}
		if len(c.Skills) > maxSkills {
			return fmt.Errorf("%w: candidate %d: too many skills (%d)", ErrInvalidCandidate, c.ID, len(c.Skills))
		}
		if uint64(c.RatingSum) > uint64(c.RatingCount)*5 {
			return fmt.Errorf("%w: candidate %d: rating sum %d exceeds 5 times count %d",
				ErrInvalidCandidate, c.ID, c.RatingSum, c.RatingCount)
		}
		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("%w: duplicate candidate id %d", ErrInvalidCandidate, c.ID)
		}
		seen[c.ID] = struct{}{}

		ids[i] = c.ID
		statics[i] = staticRec{
			hourlyRate:  c.HourlyRate,
			ratingScore: uint16(ratingScore(c.RatingSum, c.RatingCount)),
			statusScore: uint16(statusScore(c.Status)),
		}
		if i == 0 || c.HourlyRate < minRate {
			minRate = c.HourlyRate
		}
		if i == 0 || c.HourlyRate > maxRate {
			maxRate = c.HourlyRate
		}

		row := skills[i*maxSkills : (i+1)*maxSkills]
		copy(row, c.Skills)
		sort.Slice(row[:len(c.Skills)], func(a, b int) bool { return row[a] < row[b] })
		count := 0
		for j, sk := range row[:len(c.Skills)] {
			if j == 0 || sk != row[j-1] {
				row[count] = sk
				count++
			}
		}
		skillCounts[i] = uint8(count)
	}

	s.ids = ids
	s.statics = statics
	s.skillCounts = skillCounts
	s.skills = skills
	s.minRate = minRate
	s.maxRate = maxRate
	return nil
}

// Len 返回已加载的候选人数。
func (s *Store) Len() int {
	return len(s.ids)
}
