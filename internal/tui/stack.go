package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/kalverra/pronto/internal/model"
)

// PRStatus represents the pipeline status of a PR within a stack.
type PRStatus int

const (
	// StatusNeedsReview means the PR awaits review.
	StatusNeedsReview PRStatus = iota
	// StatusCIFailed means CI is failing.
	StatusCIFailed
	// StatusCIRunning means CI is in progress.
	StatusCIRunning
	// StatusApproved means review approved and CI green.
	StatusApproved
	// StatusDraft means the PR is a work in progress.
	StatusDraft
)

// PRItem represents an item within a pull request stack.
type PRItem struct {
	Number    int
	Title     string
	Index     int
	Total     int
	Additions int
	Deletions int
	Status    PRStatus
	UpdatedAt string
	PR        model.PullRequest
}

// StackGroup represents a grouped branch dependency chain.
type StackGroup struct {
	ID       int // Root PR ID
	Repo     string
	Expanded bool
	PRs      []PRItem
}

var (
	styleGlyphReview = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5C07B"))
	styleGlyphFail   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	styleGlyphRun    = lipgloss.NewStyle().Foreground(lipgloss.Color("#61AFEF"))
	styleGlyphPass   = lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379"))
	styleGlyphDraft  = lipgloss.NewStyle().Foreground(lipgloss.Color("#5C6370"))

	styleStackTag = lipgloss.NewStyle().Foreground(lipgloss.Color("#C678DD")).Bold(true)
	styleRangeTag = lipgloss.NewStyle().Foreground(lipgloss.Color("#5C6370"))
	styleAdd      = lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379"))
	styleDel      = lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75"))
)

// DeterminePRStatus maps a PullRequest to a PRStatus enum.
func DeterminePRStatus(pr model.PullRequest) PRStatus {
	if pr.IsDraft || pr.MergeStatus.IsDraft || pr.ActionStatus() == model.ActionStatusDraft {
		return StatusDraft
	}
	if pr.Checks.IsFailing() || pr.ActionStatus() == model.ActionStatusFailingCI {
		return StatusCIFailed
	}
	if pr.Checks.IsRunning() || pr.ActionStatus() == model.ActionStatusCIRunning {
		return StatusCIRunning
	}
	if pr.ReviewDecision == "APPROVED" || pr.ActionStatus() == model.ActionStatusClean {
		return StatusApproved
	}
	return StatusNeedsReview
}

// RenderStatusGlyph renders the single-character glyph for a PRStatus.
func RenderStatusGlyph(st PRStatus) string {
	switch st {
	case StatusCIFailed:
		return styleGlyphFail.Render("✖")
	case StatusNeedsReview:
		return styleGlyphReview.Render("●")
	case StatusCIRunning:
		return styleGlyphRun.Render("◌")
	case StatusApproved:
		return styleGlyphPass.Render("✓")
	case StatusDraft:
		return styleGlyphDraft.Render("○")
	default:
		return styleGlyphReview.Render("●")
	}
}

// RenderChildStatusBadge renders the glyph and label for child rows in expanded stacks.
func RenderChildStatusBadge(pr model.PullRequest) string {
	st := DeterminePRStatus(pr)
	switch st {
	case StatusCIFailed:
		return styleGlyphFail.Render("✖ Failing CI")
	case StatusNeedsReview:
		return styleGlyphReview.Render("● Needs Review")
	case StatusCIRunning:
		return styleGlyphRun.Render("◌ CI Running")
	case StatusApproved:
		return styleGlyphPass.Render("✓ Approved")
	case StatusDraft:
		return styleGlyphDraft.Render("○ Draft")
	default:
		return styleGlyphReview.Render("● Needs Review")
	}
}

// RenderMicroStatusRibbon renders the ordered micro-status ribbon for a stack.
func RenderMicroStatusRibbon(prs []PRItem) string {
	n := len(prs)
	if n > 10 {
		var rev, fail, run, pass, draft int
		for _, pr := range prs {
			switch pr.Status {
			case StatusNeedsReview:
				rev++
			case StatusCIFailed:
				fail++
			case StatusCIRunning:
				run++
			case StatusApproved:
				pass++
			case StatusDraft:
				draft++
			}
		}
		var parts []string
		if rev > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", rev, styleGlyphReview.Render("●")))
		}
		if fail > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", fail, styleGlyphFail.Render("✖")))
		}
		if run > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", run, styleGlyphRun.Render("◌")))
		}
		if pass > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", pass, styleGlyphPass.Render("✓")))
		}
		if draft > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", draft, styleGlyphDraft.Render("○")))
		}
		return fmt.Sprintf("[ %s ]", strings.Join(parts, "  "))
	}

	glyphs := make([]string, n)
	for i, pr := range prs {
		glyphs[i] = RenderStatusGlyph(pr.Status)
	}

	if n > 6 {
		return strings.Join(glyphs, "")
	}
	return strings.Join(glyphs, " ")
}

// FormatDiff formats diff line counts into compact human-readable strings (e.g. 673 -> 673, 3900 -> 3.9k).
func FormatDiff(n int) string {
	if n >= 1000 {
		val := float64(n) / 1000.0
		return fmt.Sprintf("%.1fk", val)
	}
	return strconv.Itoa(n)
}

// RenderDiffRollup renders the additions and deletions roll-up for a stack.
func RenderDiffRollup(totalAdds, totalDels int) string {
	add := styleAdd.Render("+" + FormatDiff(totalAdds))
	del := styleDel.Render("-" + FormatDiff(totalDels))
	return fmt.Sprintf("%s %s", add, del)
}

func renderStackBanner(rootPR model.PullRequest, prs []PRItem, isSelected bool, width int) string {
	var toggle string
	if isSelected {
		toggle = cursorStyle.Render("❯ ▾ ")
	} else {
		toggle = faintStyle.Render("  ▾ ")
	}

	num := numStyle.Render(fmt.Sprintf("#%d", rootPR.Number))
	tag := styleStackTag.Render("⎘ STACK")

	firstPR := prs[0]
	lastPR := prs[len(prs)-1]
	rangeText := styleRangeTag.Render(fmt.Sprintf("(%d PRs · #%d..#%d)", len(prs), firstPR.Number, lastPR.Number))
	foldHint := styleRangeTag.Render("[space: fold]")

	headerText := fmt.Sprintf("%s%s  %s %s ", toggle, num, tag, rangeText)
	textWidth := lipgloss.Width(headerText) + lipgloss.Width(foldHint) + 1
	ruleLen := max(0, width-textWidth)
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color("#3B4252")).Render(strings.Repeat("─", ruleLen))

	return fmt.Sprintf("%s%s %s", headerText, rule, foldHint)
}
