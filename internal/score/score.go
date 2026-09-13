// Package score calculates priority rankings and explainable scoring breakdowns for pull requests.
package score

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// SizeBucket categorizes PR size based on lines changed.
type SizeBucket int

// Size bucket constants based on total diff lines.
const (
	SizeXS SizeBucket = iota
	SizeS
	SizeM
	SizeL
	SizeXL
)

// String returns the size bucket label: "XS", "S", "M", "L", "XL".
func (b SizeBucket) String() string {
	switch b {
	case SizeXS:
		return "XS"
	case SizeS:
		return "S"
	case SizeM:
		return "M"
	case SizeL:
		return "L"
	case SizeXL:
		return "XL"
	default:
		return ""
	}
}

// DiffSizeBucket returns the SizeBucket for given additions and deletions.
func DiffSizeBucket(additions, deletions int) SizeBucket {
	totalLines := additions + deletions
	switch {
	case totalLines < 10:
		return SizeXS
	case totalLines < 100:
		return SizeS
	case totalLines < 500:
		return SizeM
	case totalLines < 1000:
		return SizeL
	default:
		return SizeXL
	}
}

// ExpertiseIndex calculates path ownership or familiarity.
type ExpertiseIndex interface {
	Score(path string) float64
}

// Signals extracted from a PR for ranking.
type Signals struct {
	WaitHours        float64
	AuthorIdle       bool
	IsDraft          bool
	ExpertiseRatio   float64
	SizeBucket       SizeBucket
	CIFailing        bool
	CIRunning        bool
	IsMergeable      bool
	HasMergeConflict bool
	TouchesCritical  bool
	HasNewCommits    bool
}

// Term represents a single scoring factor's contribution.
type Term struct {
	Name         string  `json:"name"`
	Raw          float64 `json:"raw"`
	Weight       float64 `json:"weight"`
	Contribution float64 `json:"contribution"`
}

// Breakdown gives transparent visibility into score calculations.
type Breakdown struct {
	Total float64 `json:"total"`
	Terms []Term  `json:"terms"`
}

// Scored wraps a PullRequest with its computed score and breakdown.
type Scored struct {
	PR        model.PullRequest `json:"pr"`
	Score     float64           `json:"score"`
	Breakdown Breakdown         `json:"breakdown"`
}

// Weights configures scoring multipliers and penalties.
type Weights struct {
	WaitHours        float64
	AuthorIdle       float64
	AuthorActive     float64
	Expertise        float64
	SizeXS           float64
	SizeS            float64
	SizeM            float64
	SizeL            float64
	SizeXL           float64
	CIFailing        float64
	CIRunning        float64
	IsMergeable      float64
	HasMergeConflict float64
	TouchesCritical  float64
	HasNewCommits    float64
	CriticalGlobs    []string
}

// DefaultWeights returns baseline scoring configuration.
func DefaultWeights() Weights {
	return Weights{
		WaitHours:        1.5,
		AuthorIdle:       20.0,
		AuthorActive:     -15.0,
		Expertise:        25.0,
		SizeXS:           20.0,
		SizeS:            10.0,
		SizeM:            0.0,
		SizeL:            -10.0,
		SizeXL:           -25.0,
		CIFailing:        -30.0,
		CIRunning:        -10.0,
		IsMergeable:      15.0,
		HasMergeConflict: -40.0,
		TouchesCritical:  25.0,
		HasNewCommits:    10.0,
		CriticalGlobs: []string{
			"go.mod",
			"go.sum",
			".github/**",
			"security/**",
		},
	}
}

// ComputeSignals extracts scoring signals from a PR.
func ComputeSignals(
	pr model.PullRequest,
	now time.Time,
	viewer string,
	teams []string,
	idx ExpertiseIndex,
	criticalGlobs []string,
) Signals {
	sig := Signals{
		WaitHours:  pr.WaitHours(now, viewer, teams),
		AuthorIdle: pr.AuthorIdle(viewer, teams),
		IsDraft:    pr.IsDraft,
	}

	if idx != nil && len(pr.Files) > 0 {
		var total float64
		for _, f := range pr.Files {
			total += idx.Score(f)
		}
		sig.ExpertiseRatio = total / float64(len(pr.Files))
	}

	sig.SizeBucket = DiffSizeBucket(pr.Additions, pr.Deletions)

	sig.CIFailing = pr.Checks.IsFailing()
	sig.CIRunning = pr.Checks.IsRunning()

	ms := model.ComputeMergeStatus(pr.Mergeable, pr.MergeStateStatus, pr.IsDraft)
	sig.IsMergeable = ms.IsClean()
	sig.HasMergeConflict = ms.HasConflict()

	for _, f := range pr.Files {
		if matchesAnyGlob(f, criticalGlobs) {
			sig.TouchesCritical = true
			break
		}
	}

	sig.HasNewCommits = pr.HasNewCommitsSinceReview(viewer)

	return sig
}

func matchesAnyGlob(p string, globs []string) bool {
	for _, g := range globs {
		if matchGlob(g, p) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, p string) bool {
	if pattern == p {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	if parts := strings.Split(pattern, "**"); len(parts) == 2 {
		if strings.HasPrefix(p, parts[0]) && strings.HasSuffix(p, parts[1]) {
			return true
		}
	}
	if matched, err := path.Match(pattern, p); err == nil && matched {
		return true
	}
	return false
}

// Explain computes the scoring breakdown for a single PR.
func Explain(
	pr model.PullRequest,
	now time.Time,
	viewer string,
	teams []string,
	idx ExpertiseIndex,
	w Weights,
) Breakdown {
	signals := ComputeSignals(pr, now, viewer, teams, idx, w.CriticalGlobs)

	var terms []Term

	if signals.WaitHours > 0 {
		terms = append(terms, Term{
			Name:         "WaitHours",
			Raw:          signals.WaitHours,
			Weight:       w.WaitHours,
			Contribution: signals.WaitHours * w.WaitHours,
		})
	}

	if signals.WaitHours > 0 {
		if signals.AuthorIdle {
			terms = append(terms, Term{
				Name:         "AuthorIdle",
				Raw:          1.0,
				Weight:       w.AuthorIdle,
				Contribution: w.AuthorIdle,
			})
		} else {
			terms = append(terms, Term{
				Name:         "AuthorActive",
				Raw:          1.0,
				Weight:       w.AuthorActive,
				Contribution: w.AuthorActive,
			})
		}
	}

	if signals.ExpertiseRatio > 0 {
		terms = append(terms, Term{
			Name:         "Expertise",
			Raw:          signals.ExpertiseRatio,
			Weight:       w.Expertise,
			Contribution: signals.ExpertiseRatio * w.Expertise,
		})
	}

	switch signals.SizeBucket {
	case SizeXS:
		terms = append(terms, Term{
			Name:         "SizeXS",
			Raw:          1.0,
			Weight:       w.SizeXS,
			Contribution: w.SizeXS,
		})
	case SizeS:
		terms = append(terms, Term{
			Name:         "SizeS",
			Raw:          1.0,
			Weight:       w.SizeS,
			Contribution: w.SizeS,
		})
	case SizeM:
		terms = append(terms, Term{
			Name:         "SizeM",
			Raw:          1.0,
			Weight:       w.SizeM,
			Contribution: w.SizeM,
		})
	case SizeL:
		terms = append(terms, Term{
			Name:         "SizeL",
			Raw:          1.0,
			Weight:       w.SizeL,
			Contribution: w.SizeL,
		})
	case SizeXL:
		terms = append(terms, Term{
			Name:         "SizeXL",
			Raw:          1.0,
			Weight:       w.SizeXL,
			Contribution: w.SizeXL,
		})
	}

	if signals.CIFailing {
		terms = append(terms, Term{
			Name:         "CIFailing",
			Raw:          1.0,
			Weight:       w.CIFailing,
			Contribution: w.CIFailing,
		})
	}

	if signals.CIRunning {
		terms = append(terms, Term{
			Name:         "CIRunning",
			Raw:          1.0,
			Weight:       w.CIRunning,
			Contribution: w.CIRunning,
		})
	}

	if signals.IsMergeable {
		terms = append(terms, Term{
			Name:         "IsMergeable",
			Raw:          1.0,
			Weight:       w.IsMergeable,
			Contribution: w.IsMergeable,
		})
	}

	if signals.HasMergeConflict {
		terms = append(terms, Term{
			Name:         "HasMergeConflict",
			Raw:          1.0,
			Weight:       w.HasMergeConflict,
			Contribution: w.HasMergeConflict,
		})
	}

	if signals.TouchesCritical {
		terms = append(terms, Term{
			Name:         "TouchesCritical",
			Raw:          1.0,
			Weight:       w.TouchesCritical,
			Contribution: w.TouchesCritical,
		})
	}

	if signals.HasNewCommits {
		terms = append(terms, Term{
			Name:         "HasNewCommits",
			Raw:          1.0,
			Weight:       w.HasNewCommits,
			Contribution: w.HasNewCommits,
		})
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

// Rank scores and orders pull requests, excluding drafts from inbox ranking.
func Rank(
	prs []model.PullRequest,
	now time.Time,
	viewer string,
	teams []string,
	idx ExpertiseIndex,
	w Weights,
) []Scored {
	scored := make([]Scored, 0, len(prs))
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		b := Explain(pr, now, viewer, teams, idx, w)
		scored = append(scored, Scored{
			PR:        pr,
			Score:     b.Total,
			Breakdown: b,
		})
	}

	type stackMeta struct {
		effectiveCat model.InboxCategory
		bottomPos    int
		bottomScore  float64
		bottomNumber int
	}

	stackMetas := make(map[string]*stackMeta)
	for i := range scored {
		pr := scored[i].PR
		if !pr.IsPartOfStack() {
			continue
		}
		key := pr.StackKey()
		cat := pr.InboxCategory(now)
		meta, exists := stackMetas[key]
		if !exists {
			stackMetas[key] = &stackMeta{
				effectiveCat: cat,
				bottomPos:    pr.Stack.Position,
				bottomScore:  scored[i].Score,
				bottomNumber: pr.Number,
			}
		} else {
			if cat < meta.effectiveCat {
				meta.effectiveCat = cat
			}
			if pr.Stack.Position < meta.bottomPos ||
				(pr.Stack.Position == meta.bottomPos && pr.Number < meta.bottomNumber) {
				meta.bottomPos = pr.Stack.Position
				meta.bottomScore = scored[i].Score
				meta.bottomNumber = pr.Number
			}
		}
	}

	getItemMeta := func(s Scored) (model.InboxCategory, float64, int, string, int) {
		if s.PR.IsPartOfStack() {
			key := s.PR.StackKey()
			if meta, ok := stackMetas[key]; ok {
				return meta.effectiveCat, meta.bottomScore, meta.bottomNumber, key, s.PR.Stack.Position
			}
		}
		return s.PR.InboxCategory(now), s.Score, s.PR.Number, "", 0
	}

	slices.SortFunc(scored, func(a, b Scored) int {
		aCat, aGrpScore, aGrpNum, aKey, aPos := getItemMeta(a)
		bCat, bGrpScore, bGrpNum, bKey, bPos := getItemMeta(b)

		if aCat != bCat {
			return cmp.Compare(aCat, bCat)
		}
		if aKey != "" && aKey == bKey {
			if aPos != bPos {
				return cmp.Compare(aPos, bPos)
			}
			return cmp.Compare(a.PR.Number, b.PR.Number)
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
