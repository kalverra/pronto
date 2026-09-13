package score

import (
	"cmp"
	"slices"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// MineWeights configures scoring weights for authored pull requests in the Mine view.
type MineWeights struct {
	BaseChangesRequested float64
	BaseCIFailing        float64
	BaseConflict         float64
	BaseQueued           float64
	BaseClean            float64
	BaseBehind           float64
	BaseNeedsReview      float64
	BaseCIRunning        float64
	BaseBlocked          float64
	BaseDraft            float64
	BaseStale            float64

	RecencyMaxHours float64
	RecencyMaxScore float64

	SizeXS float64
	SizeS  float64
	SizeM  float64
	SizeL  float64
	SizeXL float64
}

// DefaultMineWeights returns baseline scoring configuration for authored PRs.
func DefaultMineWeights() MineWeights {
	return MineWeights{
		BaseChangesRequested: 500.0,
		BaseCIFailing:        480.0,
		BaseConflict:         460.0,
		BaseQueued:           380.0,
		BaseClean:            360.0,
		BaseBehind:           330.0,
		BaseNeedsReview:      260.0,
		BaseCIRunning:        240.0,
		BaseBlocked:          220.0,
		BaseDraft:            100.0,
		BaseStale:            0.0,

		RecencyMaxHours: 168.0, // 7 days
		RecencyMaxScore: 50.0,

		SizeXS: 5.0,
		SizeS:  2.5,
		SizeM:  0.0,
		SizeL:  -2.5,
		SizeXL: -5.0,
	}
}

// ExplainMine computes scoring breakdown for an authored pull request.
func ExplainMine(pr model.PullRequest, now time.Time, w MineWeights) Breakdown {
	if pr.MergeStatus.Badge() == "" && (pr.Mergeable != "" || pr.MergeStateStatus != "" || pr.IsInMergeQueue) {
		pr.MergeStatus = model.ComputeMergeStatus(pr.Mergeable, pr.MergeStateStatus, pr.IsDraft)
		pr.MergeStatus.IsInMergeQueue = pr.IsInMergeQueue
	}

	terms := []Term{mineStatusTerm(pr, now, w)}

	if recency := mineRecencyTerm(pr, now); recency != nil {
		terms = append(terms, *recency)
	}

	if size := mineSizeTerm(pr, w); size != nil {
		terms = append(terms, *size)
	}

	var total float64
	for _, term := range terms {
		total += term.Contribution
	}

	return Breakdown{
		Total: total,
		Terms: terms,
	}
}

func mineStatusTerm(pr model.PullRequest, now time.Time, w MineWeights) Term {
	var statusName string
	var baseScore float64

	switch {
	case pr.IsStale(now):
		statusName = "Status:STALE"
		baseScore = w.BaseStale
	case pr.IsDraft || pr.MergeStatus.IsDraft:
		statusName = "Status:DRAFT"
		baseScore = w.BaseDraft
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		statusName = "Status:CHANGES_REQUESTED"
		baseScore = w.BaseChangesRequested
	case pr.Checks.IsFailing():
		statusName = "Status:FAILING_CI"
		baseScore = w.BaseCIFailing
	case pr.MergeStatus.HasConflict() || pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY":
		statusName = "Status:CONFLICT"
		baseScore = w.BaseConflict
	case pr.InMergeQueue():
		statusName = "Status:QUEUED"
		baseScore = w.BaseQueued
	case pr.MergeStatus.IsClean() || pr.MergeStateStatus == "CLEAN":
		statusName = "Status:CLEAN"
		baseScore = w.BaseClean
	case pr.MergeStatus.IsBehind() || pr.MergeStateStatus == "BEHIND":
		statusName = "Status:BEHIND"
		baseScore = w.BaseBehind
	case pr.Checks.IsRunning():
		statusName = "Status:CI_RUNNING"
		baseScore = w.BaseCIRunning
	case pr.ReviewDecision == "REVIEW_REQUIRED":
		statusName = "Status:NEEDS_REVIEW"
		baseScore = w.BaseNeedsReview
	case pr.MergeStatus.IsBlocked() || pr.MergeStateStatus == "BLOCKED":
		statusName = "Status:BLOCKED"
		baseScore = w.BaseBlocked
	default:
		statusName = "Status:NEEDS_REVIEW"
		baseScore = w.BaseNeedsReview
	}

	return Term{
		Name:         statusName,
		Raw:          1.0,
		Weight:       baseScore,
		Contribution: baseScore,
	}
}

func mineRecencyTerm(pr model.PullRequest, now time.Time) *Term {
	if pr.UpdatedAt.IsZero() {
		return nil
	}
	hoursAgo := max(now.Sub(pr.UpdatedAt).Hours(), 0)
	var recencyContrib float64
	switch {
	case hoursAgo < 4:
		recencyContrib = 50.0
	case hoursAgo < 12:
		recencyContrib = 40.0
	case hoursAgo < 24:
		recencyContrib = 30.0
	case hoursAgo < 72:
		recencyContrib = 20.0
	case hoursAgo < 168:
		recencyContrib = 10.0
	default:
		recencyContrib = 0.0
	}
	return &Term{
		Name:         "Recency",
		Raw:          hoursAgo,
		Weight:       1.0,
		Contribution: recencyContrib,
	}
}

func mineSizeTerm(pr model.PullRequest, w MineWeights) *Term {
	sizeBucket := DiffSizeBucket(pr.Additions, pr.Deletions)
	var sizeWeight float64
	switch sizeBucket {
	case SizeXS:
		sizeWeight = w.SizeXS
	case SizeS:
		sizeWeight = w.SizeS
	case SizeM:
		sizeWeight = w.SizeM
	case SizeL:
		sizeWeight = w.SizeL
	case SizeXL:
		sizeWeight = w.SizeXL
	}
	if sizeWeight == 0 {
		return nil
	}
	return &Term{
		Name:         "Size",
		Raw:          float64(pr.Additions + pr.Deletions),
		Weight:       sizeWeight,
		Contribution: sizeWeight,
	}
}

// RankMine scores and orders authored pull requests.
func RankMine(prs []model.PullRequest, now time.Time, w MineWeights) []Scored {
	scored := make([]Scored, len(prs))
	for i, pr := range prs {
		if pr.MergeStatus.Badge() == "" && (pr.Mergeable != "" || pr.MergeStateStatus != "" || pr.IsInMergeQueue) {
			pr.MergeStatus = model.ComputeMergeStatus(pr.Mergeable, pr.MergeStateStatus, pr.IsDraft)
			pr.MergeStatus.IsInMergeQueue = pr.IsInMergeQueue
		}
		b := ExplainMine(pr, now, w)
		scored[i] = Scored{
			PR:        pr,
			Score:     b.Total,
			Breakdown: b,
		}
	}

	type mineStackMeta struct {
		effectiveCat model.MineCategory
		maxScore     float64
		bottomPos    int
		bottomNumber int
	}

	stackMetas := make(map[string]*mineStackMeta)
	for i := range scored {
		pr := scored[i].PR
		if !pr.IsPartOfStack() || pr.InMergeQueue() {
			continue
		}
		key := pr.StackKey()
		cat := pr.MineCategory(now)
		s := scored[i].Score

		meta, exists := stackMetas[key]
		if !exists {
			stackMetas[key] = &mineStackMeta{
				effectiveCat: cat,
				maxScore:     s,
				bottomPos:    pr.Stack.Position,
				bottomNumber: pr.Number,
			}
		} else {
			if cat < meta.effectiveCat {
				meta.effectiveCat = cat
			}
			if s > meta.maxScore {
				meta.maxScore = s
			}
			if pr.Stack.Position < meta.bottomPos ||
				(pr.Stack.Position == meta.bottomPos && pr.Number < meta.bottomNumber) {
				meta.bottomPos = pr.Stack.Position
				meta.bottomNumber = pr.Number
			}
		}
	}

	getItemMeta := func(s Scored) (model.MineCategory, float64, int, string, int) {
		if s.PR.IsPartOfStack() && !s.PR.InMergeQueue() {
			key := s.PR.StackKey()
			if meta, ok := stackMetas[key]; ok {
				return meta.effectiveCat, meta.maxScore, meta.bottomNumber, key, s.PR.Stack.Position
			}
		}
		return s.PR.MineCategory(now), s.Score, s.PR.Number, "", 0
	}

	slices.SortFunc(scored, func(a, b Scored) int {
		aCat, aGrpScore, aGrpNum, aKey, aPos := getItemMeta(a)
		bCat, bGrpScore, bGrpNum, bKey, bPos := getItemMeta(b)

		if aKey != "" && aKey == bKey {
			if aPos != bPos {
				return cmp.Compare(aPos, bPos)
			}
			return cmp.Compare(a.PR.Number, b.PR.Number)
		}
		if aCat != bCat {
			return cmp.Compare(aCat, bCat)
		}
		if aGrpScore != bGrpScore {
			if aGrpScore > bGrpScore {
				return -1
			}
			return 1
		}
		if aGrpNum != bGrpNum {
			return cmp.Compare(aGrpNum, bGrpNum)
		}
		return cmp.Compare(a.PR.Number, b.PR.Number)
	})

	return scored
}
