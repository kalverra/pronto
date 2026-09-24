package tui

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/score"
)

var (
	appStyle = lipgloss.NewStyle().Margin(1, 2)

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#30363d")).
			Padding(0, 1)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8b949e")).
				Padding(0, 1)

	tabSeparatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("238"))

	cursorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#58a6ff"))

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#ffffff"))

	unselectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#ffffff"))

	authorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	errorBannerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("9"))

	confirmPromptStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("214"))

	successBannerStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#3fb950"))

	faintStyle = lipgloss.NewStyle().
			Faint(true)

	dividerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("242"))

	modalBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#58a6ff")).
			Padding(1, 2)

	modalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#58a6ff"))

	progressBarFilledStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#58a6ff"))

	progressBarEmptyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("238"))

	tableBorderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("238"))

	badgeQueuedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#a371f7"))

	badgeCleanStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#3fb950"))

	badgeBehindStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#e3b341"))

	badgeConflictStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#f85149"))

	badgeBlockedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#f85149"))

	badgeDraftStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#8b949e"))

	ciPassStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#3fb950"))

	ciRunningStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#e3b341"))

	ciFailStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#f85149"))

	ciNeutralStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("245"))

	ciDurationStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	botBadgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a5b4fc")).
			Faint(true)

	repoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	updatedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	diffAddStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#57ab5a"))

	diffDelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#c9514c"))

	numStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280"))

	accentCoral    = lipgloss.Color("#f0883e")
	accentRed      = lipgloss.Color("#f85149")
	accentViolet   = lipgloss.Color("#a371f7")
	accentGreen    = lipgloss.Color("#3fb950")
	accentBlue     = lipgloss.Color("#58a6ff")
	accentCharcoal = lipgloss.Color("#6e7681")
)

type rowKind int

const (
	rowItem rowKind = iota
	rowDivider
	rowStackBanner
)

type displayRow struct {
	kind             rowKind
	itemIndex        int
	dividerText      string
	dividerTitle     string
	dividerCount     int
	dividerAccent    lipgloss.TerminalColor
	stackPrefix      string
	isCollapsedStack bool
	isChildInStack   bool
	isStackRoot      bool
	stackGroup       *StackGroup
}

func (m Model) buildDisplayRows(tab Tab) []displayRow {
	var list []score.Scored
	switch tab {
	case TabFocus:
		list = m.focusItems
	case TabMine:
		list = m.mineItems
	case TabInbox:
		list = m.inboxItems
	}
	if len(list) == 0 {
		return nil
	}

	refTime := m.refTime()

	switch tab {
	case TabFocus:
		return m.buildFocusDisplayRows(list, refTime)
	case TabInbox:
		return m.buildInboxDisplayRows(list, refTime)
	case TabMine:
		return m.buildMineDisplayRows(list, refTime)
	default:
		return nil
	}
}

func (m Model) buildCategoryDisplayRows(list []score.Scored, indices []int) []displayRow {
	if len(indices) == 0 {
		return nil
	}
	stackIndices := make(map[string][]int)
	for _, idx := range indices {
		pr := list[idx].PR
		if pr.IsPartOfStack() {
			stackIndices[pr.StackKey()] = append(stackIndices[pr.StackKey()], idx)
		}
	}

	seenInStack := make(map[string]int)
	rows := make([]displayRow, 0, len(indices))
	for _, idx := range indices {
		pr := list[idx].PR
		if !pr.IsPartOfStack() {
			rows = append(rows, displayRow{
				kind:      rowItem,
				itemIndex: idx,
			})
			continue
		}

		key := pr.StackKey()
		inCatIndices := stackIndices[key]
		totalInCat := len(inCatIndices)
		posInCat := seenInStack[key]
		seenInStack[key]++

		if totalInCat <= 1 {
			meta := fmt.Sprintf("%s %s",
				styleStackTag.Render("⎘"),
				styleRangeTag.Render(fmt.Sprintf("[%d/%d]", pr.Stack.Position, pr.Stack.Size)),
			)
			rows = append(rows, displayRow{
				kind:        rowItem,
				itemIndex:   idx,
				stackPrefix: meta,
			})
			continue
		}

		if !m.isStackExpanded(key) {
			if posInCat == 0 {
				prItems := make([]PRItem, len(inCatIndices))
				for i, catIdx := range inCatIndices {
					cpr := list[catIdx].PR
					prItems[i] = PRItem{
						Number:    cpr.Number,
						Title:     cpr.Title,
						Index:     cpr.Stack.Position,
						Total:     cpr.Stack.Size,
						Additions: cpr.Additions,
						Deletions: cpr.Deletions,
						Status:    DeterminePRStatus(cpr),
						CIStatus:  DeterminePRCIStatus(cpr),
						PR:        cpr,
					}
				}
				sg := &StackGroup{
					ID:       pr.Number,
					Repo:     pr.RepoName,
					Expanded: false,
					PRs:      prItems,
				}
				rows = append(rows, displayRow{
					kind:             rowItem,
					itemIndex:        idx,
					isCollapsedStack: true,
					stackGroup:       sg,
				})
			}
			continue
		}

		if posInCat == 0 {
			prItems := make([]PRItem, len(inCatIndices))
			for i, catIdx := range inCatIndices {
				cpr := list[catIdx].PR
				prItems[i] = PRItem{
					Number:    cpr.Number,
					Title:     cpr.Title,
					Index:     cpr.Stack.Position,
					Total:     cpr.Stack.Size,
					Additions: cpr.Additions,
					Deletions: cpr.Deletions,
					Status:    DeterminePRStatus(cpr),
					CIStatus:  DeterminePRCIStatus(cpr),
					PR:        cpr,
				}
			}
			sg := &StackGroup{
				ID:       pr.Number,
				Repo:     pr.RepoName,
				Expanded: true,
				PRs:      prItems,
			}
			rows = append(rows, displayRow{
				kind:       rowStackBanner,
				itemIndex:  idx,
				stackGroup: sg,
			})
		}

		var glyph string
		if posInCat == totalInCat-1 {
			glyph = "╰─ "
		} else {
			glyph = "├─ "
		}
		prefix := fmt.Sprintf("%s#%d [%d/%d] ", glyph, pr.Number, pr.Stack.Position, pr.Stack.Size)
		rows = append(rows, displayRow{
			kind:           rowItem,
			itemIndex:      idx,
			stackPrefix:    prefix,
			isChildInStack: true,
			isStackRoot:    posInCat == 0,
		})
	}
	return rows
}

func (m Model) isFocused(pr model.PullRequest) bool {
	return m.IsFocused(pr.Key())
}

func (m Model) partitionFocused(indices []int, list []score.Scored) {
	sort.SliceStable(indices, func(i, j int) bool {
		iFocused := m.isFocused(list[indices[i]].PR)
		jFocused := m.isFocused(list[indices[j]].PR)
		if iFocused != jFocused {
			return iFocused
		}
		return false
	})
}

func (m Model) buildInboxDisplayRows(list []score.Scored, refTime time.Time) []displayRow {
	stackCat := make(map[string]model.InboxCategory)
	for _, item := range list {
		if item.PR.IsPartOfStack() {
			key := item.PR.StackKey()
			cat := item.PR.InboxCategory(refTime)
			if current, ok := stackCat[key]; !ok || cat < current {
				stackCat[key] = cat
			}
		}
	}

	var attentionIndices, blockedIndices, staleIndices []int
	for i, item := range list {
		cat := item.PR.InboxCategory(refTime)
		if item.PR.IsPartOfStack() {
			if eff, ok := stackCat[item.PR.StackKey()]; ok {
				cat = eff
			}
		}
		switch cat {
		case model.CategoryAttention:
			attentionIndices = append(attentionIndices, i)
		case model.CategoryBlocked:
			blockedIndices = append(blockedIndices, i)
		case model.CategoryStale:
			staleIndices = append(staleIndices, i)
		}
	}

	m.partitionFocused(attentionIndices, list)
	m.partitionFocused(blockedIndices, list)
	m.partitionFocused(staleIndices, list)

	displayRows := make([]displayRow, 0, len(list)+3)
	if len(attentionIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "NEEDS YOUR ATTENTION",
			dividerCount:  len(attentionIndices),
			dividerAccent: accentCoral,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, attentionIndices)...)
	}
	if len(blockedIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "BLOCKED",
			dividerCount:  len(blockedIndices),
			dividerAccent: accentRed,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, blockedIndices)...)
	}
	if len(staleIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "STALE",
			dividerCount:  len(staleIndices),
			dividerAccent: accentCharcoal,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, staleIndices)...)
	}
	return displayRows
}

func (m Model) buildMineDisplayRows(list []score.Scored, refTime time.Time) []displayRow {
	stackCat := make(map[string]model.MineCategory)
	for _, item := range list {
		if item.PR.IsPartOfStack() && !item.PR.InMergeQueue() {
			key := item.PR.StackKey()
			cat := item.PR.MineCategory(refTime)
			if current, ok := stackCat[key]; !ok || cat < current {
				stackCat[key] = cat
			}
		}
	}

	var (
		actionIndices []int
		queuedIndices []int
		readyIndices  []int
		reviewIndices []int
		draftIndices  []int
		staleIndices  []int
	)
	for i, item := range list {
		cat := item.PR.MineCategory(refTime)
		if item.PR.IsPartOfStack() && !item.PR.InMergeQueue() {
			if eff, ok := stackCat[item.PR.StackKey()]; ok {
				cat = eff
			}
		}
		switch cat {
		case model.MineCategoryActionRequired:
			actionIndices = append(actionIndices, i)
		case model.MineCategoryQueued:
			queuedIndices = append(queuedIndices, i)
		case model.MineCategoryReadyToMerge:
			readyIndices = append(readyIndices, i)
		case model.MineCategoryInReview:
			reviewIndices = append(reviewIndices, i)
		case model.MineCategoryDraft:
			draftIndices = append(draftIndices, i)
		case model.MineCategoryStale:
			staleIndices = append(staleIndices, i)
		}
	}

	m.partitionFocused(actionIndices, list)
	m.partitionFocused(queuedIndices, list)
	m.partitionFocused(readyIndices, list)
	m.partitionFocused(reviewIndices, list)
	m.partitionFocused(draftIndices, list)
	m.partitionFocused(staleIndices, list)

	displayRows := make([]displayRow, 0, len(list)+6)
	if len(actionIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "ACTION REQUIRED",
			dividerCount:  len(actionIndices),
			dividerAccent: accentCoral,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, actionIndices)...)
	}
	if len(queuedIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "MERGE QUEUE",
			dividerCount:  len(queuedIndices),
			dividerAccent: accentViolet,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, queuedIndices)...)
	}
	if len(readyIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "READY TO MERGE",
			dividerCount:  len(readyIndices),
			dividerAccent: accentGreen,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, readyIndices)...)
	}
	if len(reviewIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "IN REVIEW",
			dividerCount:  len(reviewIndices),
			dividerAccent: accentBlue,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, reviewIndices)...)
	}
	if len(draftIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "DRAFTS",
			dividerCount:  len(draftIndices),
			dividerAccent: accentCharcoal,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, draftIndices)...)
	}
	if len(staleIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  "STALE",
			dividerCount:  len(staleIndices),
			dividerAccent: accentCharcoal,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, staleIndices)...)
	}
	return displayRows
}

type focusSection int

const (
	focusSectionAttention focusSection = iota
	focusSectionActionRequired
	focusSectionMergeQueue
	focusSectionReadyToMerge
	focusSectionInReview
	focusSectionBlocked
	focusSectionDrafts
	focusSectionStale
	focusSectionUnknown
)

var focusSections = []struct {
	id     focusSection
	title  string
	accent lipgloss.TerminalColor
}{
	{focusSectionAttention, "NEEDS YOUR ATTENTION", accentCoral},
	{focusSectionActionRequired, "ACTION REQUIRED", accentCoral},
	{focusSectionMergeQueue, "MERGE QUEUE", accentViolet},
	{focusSectionReadyToMerge, "READY TO MERGE", accentGreen},
	{focusSectionInReview, "IN REVIEW", accentBlue},
	{focusSectionBlocked, "BLOCKED", accentRed},
	{focusSectionDrafts, "DRAFTS", accentCharcoal},
	{focusSectionStale, "STALE", accentCharcoal},
}

func computeFocusStackCategories(
	list []score.Scored,
	isAuthored func(model.PullRequest) bool,
	refTime time.Time,
) (map[string]model.MineCategory, map[string]model.InboxCategory) {
	mineStackCat := make(map[string]model.MineCategory)
	inboxStackCat := make(map[string]model.InboxCategory)
	for _, item := range list {
		if !item.PR.IsPartOfStack() {
			continue
		}
		key := item.PR.StackKey()
		if isAuthored(item.PR) {
			if !item.PR.InMergeQueue() {
				cat := item.PR.MineCategory(refTime)
				if current, ok := mineStackCat[key]; !ok || cat < current {
					mineStackCat[key] = cat
				}
			}
		} else {
			cat := item.PR.InboxCategory(refTime)
			if current, ok := inboxStackCat[key]; !ok || cat < current {
				inboxStackCat[key] = cat
			}
		}
	}
	return mineStackCat, inboxStackCat
}

func categorizeFocusItem(
	item score.Scored,
	isAuthored bool,
	refTime time.Time,
	mineStackCat map[string]model.MineCategory,
	inboxStackCat map[string]model.InboxCategory,
) focusSection {
	if isAuthored {
		cat := item.PR.MineCategory(refTime)
		if item.PR.IsPartOfStack() && !item.PR.InMergeQueue() {
			if eff, ok := mineStackCat[item.PR.StackKey()]; ok {
				cat = eff
			}
		}
		switch cat {
		case model.MineCategoryActionRequired:
			return focusSectionActionRequired
		case model.MineCategoryQueued:
			return focusSectionMergeQueue
		case model.MineCategoryReadyToMerge:
			return focusSectionReadyToMerge
		case model.MineCategoryInReview:
			return focusSectionInReview
		case model.MineCategoryDraft:
			return focusSectionDrafts
		case model.MineCategoryStale:
			return focusSectionStale
		}
		return focusSectionUnknown
	}

	cat := item.PR.InboxCategory(refTime)
	if item.PR.IsPartOfStack() {
		if eff, ok := inboxStackCat[item.PR.StackKey()]; ok {
			cat = eff
		}
	}
	switch cat {
	case model.CategoryAttention:
		return focusSectionAttention
	case model.CategoryBlocked:
		return focusSectionBlocked
	case model.CategoryStale:
		return focusSectionStale
	}
	return focusSectionUnknown
}

func (m Model) buildFocusDisplayRows(list []score.Scored, refTime time.Time) []displayRow {
	if len(list) == 0 {
		return nil
	}

	mineKeys := make(map[model.PRKey]bool, len(m.mineItems))
	for _, it := range m.mineItems {
		mineKeys[it.PR.Key()] = true
	}
	isAuthored := func(pr model.PullRequest) bool {
		return mineKeys[pr.Key()] || (m.viewer != "" && strings.EqualFold(pr.Author, m.viewer))
	}

	mineStackCat, inboxStackCat := computeFocusStackCategories(list, isAuthored, refTime)

	sectionIndices := make(map[focusSection][]int)
	for i, item := range list {
		sec := categorizeFocusItem(item, isAuthored(item.PR), refTime, mineStackCat, inboxStackCat)
		sectionIndices[sec] = append(sectionIndices[sec], i)
	}

	displayRows := make([]displayRow, 0, len(list)+len(focusSections))
	for _, sec := range focusSections {
		indices := sectionIndices[sec.id]
		if len(indices) == 0 {
			continue
		}
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  sec.title,
			dividerCount:  len(indices),
			dividerAccent: sec.accent,
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, indices)...)
	}
	return displayRows
}

// View renders the terminal user interface.
func (m Model) View() string {
	if m.modalOpen {
		return m.renderModal()
	}
	if m.detailsOpen {
		return m.renderDetailsModal()
	}

	var b strings.Builder

	contentWidth := max(20, m.width-4)
	b.WriteString(m.renderTabBar(contentWidth))

	b.WriteString(m.renderNotificationsArea())

	// Body
	list := m.activeList()
	switch {
	case m.loading:
		if m.loadingTotal > 0 {
			b.WriteString("  Fetching pull requests…\n")
			barWidth := min(30, max(10, m.width-20))
			bar := renderProgressBar(m.loadingLoaded, m.loadingTotal, barWidth)
			fmt.Fprintf(&b, "  %s %d/%d\n\n", bar, m.loadingLoaded, m.loadingTotal)
		} else {
			b.WriteString("  Fetching pull requests…\n\n")
		}
	case len(list) == 0:
		b.WriteString("  No pull requests in this view.\n\n")
	default:
		visRows := m.VisibleRows()
		cursor := m.Cursor()

		refTime := m.refTime()

		displayRows := m.buildDisplayRows(m.activeTab)

		scroll := min(max(m.ScrollOffset(), 0), len(displayRows))
		end := min(len(displayRows), scroll+visRows)

		tableStr := m.renderPRTable(list, displayRows, scroll, end, cursor, refTime)
		b.WriteString(tableStr)
		b.WriteString("\n")

		visibleRows := displayRows[scroll:end]
		if len(displayRows) > visRows {
			shown := 0
			for _, dRow := range visibleRows {
				if dRow.kind == rowItem {
					shown++
				}
			}
			fmt.Fprintf(&b, "  showing %d of %d PRs\n", shown, len(list))
		}
	}

	// Status banner
	if status := m.renderStatusBanner(); status != "" {
		b.WriteString("\n")
		b.WriteString(status)
	}

	// Help bar
	b.WriteString("\n")
	helpText := "enter: details • ↑/↓: navigate • space/e: expand • tab: switch • o: open • f: focus • x: close stale • n: notifs • ?: why score • r: refresh • q: quit"
	if m.IsNotificationFocused() {
		helpText = "enter/o: open • ↑/↓: select • x: dismiss • esc: back to PRs • q: quit"
	}
	b.WriteString(renderHelp(helpText))
	b.WriteString("\n")

	return appStyle.Render(b.String())
}

// renderPRTable renders the PR list table with left-aligned columns and progressive width budgeting.
func (m Model) renderPRTable(
	list []score.Scored,
	displayRows []displayRow,
	scroll, end, cursor int,
	refTime time.Time,
) string {
	headers := []string{"", "TITLE", "STATUS", "SIZE", "CI", "UPDATED", "REPO"}
	hasAuthor := m.activeTab != TabMine
	if hasAuthor {
		headers = append(headers, "AUTHOR")
	}

	visibleRows := displayRows[scroll:end]
	maxNumWidth := 0
	maxStackWidth := 0
	for _, dRow := range displayRows {
		if dRow.kind != rowItem || dRow.isChildInStack {
			continue
		}
		pr := list[dRow.itemIndex].PR
		numStr := fmt.Sprintf("#%d", pr.Number)
		if len(numStr) > maxNumWidth {
			maxNumWidth = len(numStr)
		}

		if dRow.isCollapsedStack && dRow.stackGroup != nil {
			stackMeta := formatCollapsedStackMeta(len(dRow.stackGroup.PRs))
			if w := lipgloss.Width(stackMeta); w > maxStackWidth {
				maxStackWidth = w
			}
		} else if dRow.stackPrefix != "" {
			if w := lipgloss.Width(dRow.stackPrefix); w > maxStackWidth {
				maxStackWidth = w
			}
		}
	}

	var rows [][]string
	for _, dRow := range visibleRows {
		if dRow.kind == rowDivider || dRow.kind == rowStackBanner {
			rows = append(rows, make([]string, len(headers)))
			continue
		}

		item := list[dRow.itemIndex]
		isSelected := dRow.itemIndex == cursor && !m.IsNotificationFocused()

		var rowCols []string
		switch {
		case dRow.isCollapsedStack && dRow.stackGroup != nil:
			rowCols = m.collapsedStackRowCols(dRow.stackGroup, item.PR, isSelected, refTime, maxNumWidth, maxStackWidth)
		case dRow.isChildInStack:
			rowCols = m.childStackRowCols(dRow, item.PR, isSelected, refTime)
		default:
			rowCols = m.itemRowCols(dRow, item.PR, isSelected, refTime, maxNumWidth, maxStackWidth)
		}
		if hasAuthor {
			rowCols = append(rowCols, renderAuthor(item.PR))
		}
		rows = append(rows, rowCols)
	}

	totalWidth := max(20, m.width-4)
	cw := computeTableColumnWidths(totalWidth, hasAuthor, rows)

	tbl := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(tableBorderStyle).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderHeader(true).
		Headers(headers...).
		Rows(rows...).
		Wrap(false)

	tbl.StyleFunc(func(row, col int) lipgloss.Style {
		s := lipgloss.NewStyle().Align(lipgloss.Left)
		switch col {
		case 0:
			s = s.Width(cw.cursor)
		case 1:
			s = s.Width(cw.title).Padding(0, 1)
		case 2:
			s = s.Width(cw.status).Padding(0, 1)
		case 3:
			s = s.Width(cw.size).Padding(0, 1)
		case 4:
			s = s.Width(cw.ci).Padding(0, 1)
		case 5:
			s = s.Width(cw.updated).Padding(0, 1)
		case 6:
			s = s.Width(cw.repo).Padding(0, 1)
		case 7:
			s = s.Width(cw.author).Padding(0, 1)
		}
		if row == table.HeaderRow {
			return s.Bold(true).Foreground(lipgloss.Color("245"))
		}
		return s
	})

	rendered := tbl.Render()
	lines := strings.Split(rendered, "\n")
	tblWidth := 0
	if len(lines) > 1 {
		tblWidth = lipgloss.Width(lines[1])
	}
	if tblWidth <= 0 {
		tblWidth = totalWidth
	}
	m.rewriteSpecialRowLines(lines, visibleRows, list, cursor, tblWidth)
	return strings.Join(lines, "\n")
}

type tableColumnWidths struct {
	cursor  int
	title   int
	status  int
	size    int
	ci      int
	updated int
	repo    int
	author  int
}

// computeTableColumnWidths dynamically computes column widths for the PR table.
// It measures the actual content width needed across visible rows, left-aligns,
// and as screen width decreases, progressively truncates columns
// while keeping all columns intact and truncating the elastic TITLE column first.
func computeTableColumnWidths(totalWidth int, hasAuthor bool, rows [][]string) tableColumnWidths {
	w := tableColumnWidths{
		cursor:  3,
		status:  len("STATUS"),
		size:    len("SIZE"),
		ci:      len("CI"),
		updated: len("UPDATED"),
		repo:    len("REPO"),
	}
	if hasAuthor {
		w.author = len("AUTHOR")
	}

	for _, r := range rows {
		if len(r) > 2 {
			w.status = max(w.status, lipgloss.Width(r[2]))
		}
		if len(r) > 3 {
			w.size = max(w.size, lipgloss.Width(r[3]))
		}
		if len(r) > 4 {
			w.ci = max(w.ci, lipgloss.Width(r[4]))
		}
		if len(r) > 5 {
			w.updated = max(w.updated, lipgloss.Width(r[5]))
		}
		if len(r) > 6 {
			w.repo = max(w.repo, lipgloss.Width(r[6]))
		}
		if hasAuthor && len(r) > 7 {
			w.author = max(w.author, lipgloss.Width(r[7]))
		}
	}

	// Add 2 padding for each non-cursor column (1 space padding on left, 1 on right)
	w.status += 2
	w.size += 2
	w.ci += 2
	w.updated += 2
	w.repo += 2
	if hasAuthor {
		w.author += 2
	}

	fixedSum := func() int {
		sum := w.cursor + w.status + w.size + w.ci + w.updated + w.repo
		if hasAuthor {
			sum += w.author
		}
		return sum
	}

	availForTitle := totalWidth - fixedSum()

	// If TITLE has at least 20 chars, give all remaining width to TITLE
	if availForTitle >= 20 {
		w.title = availForTitle
		return w
	}

	// Screen is getting smaller: start progressively truncating metadata columns
	// to give room to TITLE while keeping all columns visible.
	deficit := 20 - availForTitle

	// 1. Truncate REPO down to 10
	if deficit > 0 && w.repo > 10 {
		shrink := min(deficit, w.repo-10)
		w.repo -= shrink
		deficit -= shrink
	}

	// 2. Truncate CI down to 12
	if deficit > 0 && w.ci > 12 {
		shrink := min(deficit, w.ci-12)
		w.ci -= shrink
		deficit -= shrink
	}

	// 3. Truncate AUTHOR down to 11
	if hasAuthor && deficit > 0 && w.author > 11 {
		shrink := min(deficit, w.author-11)
		w.author -= shrink
		deficit -= shrink
	}

	// 4. Truncate REPO further down to 8 if very tight
	if deficit > 0 && w.repo > 8 {
		shrink := min(deficit, w.repo-8)
		w.repo -= shrink
		deficit -= shrink
	}

	// 5. Truncate CI further down to 8 if very tight
	if deficit > 0 && w.ci > 8 {
		shrink := min(deficit, w.ci-8)
		w.ci -= shrink
		deficit -= shrink
	}

	// 6. Truncate STATUS down to 10 if very tight
	if deficit > 0 && w.status > 10 {
		shrink := min(deficit, w.status-10)
		w.status -= shrink
	}

	w.title = max(10, totalWidth-fixedSum())
	return w
}

// rewriteSpecialRowLines replaces the placeholder table lines for category
// dividers and expanded stack banners with their full-width rendered forms.
func (m Model) rewriteSpecialRowLines(
	lines []string,
	visibleRows []displayRow,
	list []score.Scored,
	cursor, tblWidth int,
) {
	for i, dRow := range visibleRows {
		lineIdx := i + 2
		if lineIdx >= len(lines) {
			continue
		}
		switch {
		case dRow.kind == rowDivider:
			if dRow.dividerTitle != "" {
				lines[lineIdx] = renderCategoryHeader(
					dRow.dividerTitle,
					dRow.dividerCount,
					dRow.dividerAccent,
					tblWidth,
				)
			} else {
				lines[lineIdx] = renderCategoryDivider(dRow.dividerText, tblWidth)
			}
		case dRow.kind == rowStackBanner && dRow.stackGroup != nil:
			rootPR := list[dRow.itemIndex].PR
			bannerSelected := dRow.itemIndex == cursor && !m.IsNotificationFocused()
			lines[lineIdx] = renderStackBanner(rootPR, dRow.stackGroup.PRs, bannerSelected, tblWidth)
		}
	}
}

func formatCollapsedStackMeta(count int) string {
	prCountStr := fmt.Sprintf("%d PRs", count)
	if count == 1 {
		prCountStr = "1 PR"
	}
	return fmt.Sprintf("%s %s", styleStackTag.Render("⎘"), prCountStr)
}

// collapsedStackRowCols renders the single collapsed stack row: a micro-status
// ribbon for the whole stack plus the summed diff roll-up.
func (m Model) collapsedStackRowCols(
	sg *StackGroup,
	root model.PullRequest,
	isSelected bool,
	refTime time.Time,
	maxNumWidth, maxStackWidth int,
) []string {
	var prefix string
	if isSelected {
		prefix = cursorStyle.Render("❯ ▸")
	} else {
		prefix = faintStyle.Render("  ▸")
	}

	totalAdds, totalDels := 0, 0
	for _, prItem := range sg.PRs {
		totalAdds += prItem.Additions
		totalDels += prItem.Deletions
	}

	stackMeta := formatCollapsedStackMeta(len(sg.PRs))
	padStack := ""
	if maxStackWidth > lipgloss.Width(stackMeta) {
		padStack = strings.Repeat(" ", maxStackWidth-lipgloss.Width(stackMeta))
	}
	stackPart := stackMeta + padStack

	rootTitle := root.Title
	if m.isFocused(root) {
		rootTitle = "★ " + rootTitle
	}

	numStr := fmt.Sprintf("#%d", root.Number)
	padNum := ""
	if maxNumWidth > len(numStr) {
		padNum = strings.Repeat(" ", maxNumWidth-len(numStr))
	}
	numText := numStyle.Render(numStr) + padNum

	titleStyle := unselectedRowStyle
	if isSelected {
		titleStyle = selectedRowStyle
	}
	titleText := fmt.Sprintf("%s  %s  %s", numText, stackPart, titleStyle.Render(rootTitle))

	var updatedText string
	if !root.UpdatedAt.IsZero() {
		d := max(refTime.Sub(root.UpdatedAt), 0)
		updatedText = updatedStyle.Render(humanAge(d))
	}

	repo := root.RepoName
	if repo == "" {
		repo = root.RepoNameWithOwner
	}

	return []string{
		prefix,
		titleText,
		RenderMicroStatusRibbon(sg.PRs),
		RenderDiffRollup(totalAdds, totalDels),
		RenderMicroCIRibbon(sg.PRs),
		updatedText,
		repoStyle.Render(repo),
	}
}

// childStackRowCols renders a single PR row inside an expanded stack.
func (m Model) childStackRowCols(dRow displayRow, pr model.PullRequest, isSelected bool, refTime time.Time) []string {
	prefix := "   "
	if isSelected && !dRow.isStackRoot {
		prefix = cursorStyle.Render("❯  ")
	}

	title := pr.Title
	if m.isFocused(pr) {
		title = "★ " + title
	}
	fullTitle := dRow.stackPrefix + title
	titleStyle := unselectedRowStyle
	if isSelected && !dRow.isStackRoot {
		titleStyle = selectedRowStyle
	}
	titleText := titleStyle.Render(fullTitle)

	var updatedText string
	if !pr.UpdatedAt.IsZero() {
		d := max(refTime.Sub(pr.UpdatedAt), 0)
		updatedText = updatedStyle.Render(humanAge(d))
	}

	repo := pr.RepoName
	if repo == "" {
		repo = pr.RepoNameWithOwner
	}

	return []string{
		prefix,
		titleText,
		RenderChildStatusBadge(pr),
		fmt.Sprintf("%s %s",
			styleAdd.Render("+"+FormatDiff(pr.Additions)),
			styleDel.Render("-"+FormatDiff(pr.Deletions)),
		),
		m.renderCIBadge(pr.Checks, refTime),
		updatedText,
		repoStyle.Render(repo),
	}
}

// itemRowCols renders a standard (non-stack, or solo-stack) PR row.
func (m Model) itemRowCols(
	dRow displayRow,
	pr model.PullRequest,
	isSelected bool,
	refTime time.Time,
	maxNumWidth, maxStackWidth int,
) []string {
	prefix := "   "
	if isSelected {
		prefix = cursorStyle.Render("❯  ")
	}

	numStr := fmt.Sprintf("#%d", pr.Number)
	padNum := ""
	if maxNumWidth > len(numStr) {
		padNum = strings.Repeat(" ", maxNumWidth-len(numStr))
	}
	numText := numStyle.Render(numStr) + padNum

	title := pr.Title
	if m.isFocused(pr) {
		title = "★ " + title
	}
	titleStyle := unselectedRowStyle
	if isSelected {
		titleStyle = selectedRowStyle
	}

	var titleText string
	switch {
	case dRow.stackPrefix != "":
		padStack := ""
		if maxStackWidth > lipgloss.Width(dRow.stackPrefix) {
			padStack = strings.Repeat(" ", maxStackWidth-lipgloss.Width(dRow.stackPrefix))
		}
		stackPart := dRow.stackPrefix + padStack
		titleText = fmt.Sprintf("%s  %s  %s", numText, stackPart, titleStyle.Render(title))
	case maxStackWidth > 0:
		padStack := strings.Repeat(" ", maxStackWidth)
		titleText = fmt.Sprintf("%s  %s  %s", numText, padStack, titleStyle.Render(title))
	default:
		titleText = fmt.Sprintf("%s  %s", numText, titleStyle.Render(title))
	}

	statusBadge := renderStatusBadge(pr)
	sizeText := renderDiffSize(pr.Additions, pr.Deletions)
	ciBadge := m.renderCIBadge(pr.Checks, refTime)
	if pr.Partial {
		statusBadge = ciRunningStyle.Render(m.spinnerChar() + " LOADING")
		sizeText = faintStyle.Render("…")
		ciBadge = ""
	}

	var updatedText string
	if !pr.UpdatedAt.IsZero() {
		d := max(refTime.Sub(pr.UpdatedAt), 0)
		updatedText = updatedStyle.Render(humanAge(d))
	}

	repo := pr.RepoName
	if repo == "" {
		repo = pr.RepoNameWithOwner
	}

	return []string{
		prefix,
		titleText,
		statusBadge,
		sizeText,
		ciBadge,
		updatedText,
		repoStyle.Render(repo),
	}
}

func (m Model) renderTabBar(contentWidth int) string {
	tabFocusTitle := fmt.Sprintf("1: Focus (%d)", len(m.focusItems))
	tabMineTitle := fmt.Sprintf("2: Mine (%d)", len(m.mineItems))
	tabInboxTitle := fmt.Sprintf("3: Inbox (%d)", len(m.inboxItems))

	renderTab := func(t Tab, title string) string {
		if m.activeTab == t {
			return activeTabStyle.Render(title)
		}
		return inactiveTabStyle.Render(title)
	}

	sep := tabSeparatorStyle.Render("│")
	tabs := lipgloss.JoinHorizontal(
		lipgloss.Top,
		renderTab(TabFocus, tabFocusTitle),
		sep,
		renderTab(TabMine, tabMineTitle),
		sep,
		renderTab(TabInbox, tabInboxTitle),
	)

	var notifBadge string
	if len(m.notifications) == 0 {
		notifBadge = faintStyle.Render("🔔 0")
	} else {
		alertCount := len(m.notifications)
		alertText := fmt.Sprintf("🔔 %d new alerts", alertCount)
		if alertCount == 1 {
			alertText = "🔔 1 new alert"
		}
		notifBadge = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#d29922")).Render(alertText)
	}

	rightHelp := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("[?] Help")
	rightPart := notifBadge + "  " + sep + "  " + rightHelp

	tabsWidth := lipgloss.Width(tabs)
	rightWidth := lipgloss.Width(rightPart)
	if contentWidth > tabsWidth+rightWidth+2 {
		space := strings.Repeat(" ", contentWidth-tabsWidth-rightWidth)
		tabs = tabs + space + rightPart
	} else {
		tabs = tabs + "  " + rightPart
	}

	var b strings.Builder
	b.WriteString(tabs)
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")
	return b.String()
}

func (m Model) renderStatusBanner() string {
	switch {
	case m.viewErr != nil && m.viewErrPR != nil:
		return errorBannerStyle.Render(fmt.Sprintf(
			"⚠ failed to view #%d: %s", m.viewErrPR.Number, m.viewErr))
	case m.closeErr != nil && m.closeErrPR != nil:
		return errorBannerStyle.Render(fmt.Sprintf(
			"⚠ failed to close #%d: %s", m.closeErrPR.Number, m.closeErr))
	case m.confirmClosePR != nil:
		return confirmPromptStyle.Render(fmt.Sprintf(
			"Close #%d as stale? [y/N]", m.confirmClosePR.Number))
	case m.closingPR != nil:
		return faintStyle.Render(fmt.Sprintf(
			"closing #%d as stale…", m.closingPR.Number))
	case m.lastClosedPR != nil:
		return successBannerStyle.Render(fmt.Sprintf(
			"closed #%d as stale", m.lastClosedPR.Number))
	case m.fetchErr != nil:
		if m.lastFetch.IsZero() {
			return errorBannerStyle.Render(fmt.Sprintf(
				"⚠ refresh failed: %s — no data yet", m.fetchErr))
		}
		return errorBannerStyle.Render(fmt.Sprintf(
			"⚠ refresh failed: %s — data from %s ago", m.fetchErr, humanAge(time.Since(m.lastFetch))))
	case !m.loading && m.loadingTotal > 0 && m.loadingLoaded < m.loadingTotal:
		barWidth := min(20, max(8, m.width-40))
		bar := renderProgressBar(m.loadingLoaded, m.loadingTotal, barWidth)
		return faintStyle.Render(fmt.Sprintf(
			"refreshing… %s %d/%d", bar, m.loadingLoaded, m.loadingTotal))
	case m.refreshing:
		return faintStyle.Render("refreshing…")
	case m.staleSnapshot:
		return faintStyle.Render("stale data — refreshing…")
	case m.notifyHint() != "":
		return faintStyle.Render(m.notifyHint())
	default:
		return ""
	}
}

// notifyHint explains a degraded desktop notifier (see
// notify.FallbackNotifier.Health) and how to fix it, or "" when healthy.
func (m Model) notifyHint() string {
	h, ok := m.notifier.(interface{ Health() error })
	if !ok {
		return ""
	}
	switch err := h.Health(); {
	case err == nil:
		return ""
	case errors.Is(err, notify.ErrDenied):
		return "🔔 Pronto notifications are off in System Settings > Notifications — using terminal fallback"
	case errors.Is(err, notify.ErrNotAuthorized):
		return "🔔 Pronto notifications not authorized — using terminal fallback; run `pronto notify setup`"
	case errors.Is(err, notify.ErrHelperStale):
		return "🔔 notification helper out of date — run `pronto notify setup`"
	case errors.Is(err, notify.ErrHelperNotFound):
		return "🔔 native notifications not set up — run `pronto notify setup` " +
			"(or set notifications.mode = \"terminal\")"
	default:
		return ""
	}
}

func (m Model) renderNotificationsArea() string {
	if len(m.notifications) == 0 {
		return ""
	}

	if m.hideNotifs {
		return faintStyle.Render(fmt.Sprintf("  ▸ 🔔 %d unread [n]\n\n", len(m.notifications)))
	}

	width := max(40, m.width-4)
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	if m.IsNotificationFocused() {
		borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff"))
	}

	titleText := fmt.Sprintf("─ 🔔 NOTIFICATIONS (%d) ", len(m.notifications))
	titleStyled := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Render(titleText)
	hintStyled := faintStyle.Render(" [n to hide] ─")

	ruleLen := max(width-2-lipgloss.Width(titleStyled)-lipgloss.Width(hintStyled), 0)
	rule := borderStyle.Render(strings.Repeat("─", ruleLen))

	topLine := borderStyle.Render("╭") + titleStyled + rule + hintStyled + borderStyle.Render("╮")

	var rows strings.Builder
	innerWidth := max(10, width-4)
	for i, n := range m.notifications {
		prefix := "  "
		if m.IsNotificationFocused() && i == m.notificationCursor {
			prefix = cursorStyle.Render("> ")
		}

		badge := notificationBadge(n)
		ref, details := formatNotificationContent(n)

		age := "0s"
		if !n.SubmittedAt.IsZero() {
			d := max(m.currentTime().Sub(n.SubmittedAt), 0)
			age = humanAge(d)
		}
		ts := faintStyle.Render("· " + age + " ago")

		var rowParts []string
		if ref != "" {
			rowParts = append(rowParts, repoStyle.Render(ref))
		}
		if details != "" {
			rowParts = append(rowParts, details)
		}
		rowParts = append(rowParts, ts)

		content := strings.Join(rowParts, "  ")
		innerContent := fmt.Sprintf("%s%s  %s", prefix, badge, content)

		padLen := max(innerWidth-lipgloss.Width(innerContent), 0)

		rowLine := borderStyle.Render(
			"│",
		) + " " + innerContent + strings.Repeat(
			" ",
			padLen,
		) + " " + borderStyle.Render(
			"│",
		)
		rows.WriteString(rowLine)
		rows.WriteString("\n")
	}

	bottomLine := borderStyle.Render("╰" + strings.Repeat("─", width-2) + "╯")

	return topLine + "\n" + rows.String() + bottomLine + "\n\n"
}

func formatNotificationContent(n notify.Notification) (string, string) {
	ref := n.Repo
	if ref != "" && n.PRNumber > 0 {
		ref = fmt.Sprintf("%s#%d", ref, n.PRNumber)
	} else if n.PRNumber > 0 {
		ref = fmt.Sprintf("#%d", n.PRNumber)
	}

	var details string
	if n.PRTitle != "" {
		details = fmt.Sprintf("%q", n.PRTitle)
		if n.Trigger == notify.TriggerReviewReceived && n.Author != "" {
			details = fmt.Sprintf("%s by @%s", details, n.Author)
		}
	} else {
		msg := n.Message
		if msg == "" {
			msg = n.Title
		}

		// Strip trailing (repo#pr) or (#pr) if present
		if idx := strings.LastIndex(msg, " ("); idx != -1 && strings.HasSuffix(msg, ")") {
			inside := msg[idx+2 : len(msg)-1]
			if strings.Contains(inside, "#") {
				if ref == "" || ref == fmt.Sprintf("#%d", n.PRNumber) {
					ref = inside
				}
				msg = strings.TrimSpace(msg[:idx])
			}
		}

		// Strip redundant prefixes
		prefixes := []string{
			"Checks passed for ",
			"Checks passed",
			"CI failed for ",
			"CI failed",
			"Merge conflict in ",
			"Merge conflict",
			"Merged: ",
			"Merged",
		}
		for _, p := range prefixes {
			if after, ok := strings.CutPrefix(msg, p); ok {
				msg = strings.TrimSpace(after)
				break
			}
		}

		// Review formatting: e.g. "@alice approved: \"Feature\"" or "@alice Changes requested"
		if n.Trigger == notify.TriggerReviewReceived {
			if n.Author != "" {
				revPrefix := fmt.Sprintf("@%s ", n.Author)
				if after, ok := strings.CutPrefix(msg, revPrefix); ok {
					rem := after
					if _, after, ok := strings.Cut(rem, ": "); ok {
						msg = strings.TrimSpace(after) + " by @" + n.Author
					} else {
						msg = "by @" + n.Author
					}
				}
			}
		}

		details = msg
	}

	return ref, details
}

func notificationBadge(n notify.Notification) string {
	var (
		badgeText  string
		badgeStyle lipgloss.Style
	)
	switch n.Trigger {
	case notify.TriggerCIFailed:
		badgeText = "✖ CI FAIL"
		badgeStyle = ciFailStyle
	case notify.TriggerConflict:
		badgeText = "✖ CONFLICT"
		badgeStyle = badgeConflictStyle
	case notify.TriggerReviewReceived:
		switch n.ReviewState {
		case "CHANGES_REQUESTED":
			badgeText = "✖ CHANGES REQ"
			badgeStyle = badgeBlockedStyle
		case "APPROVED":
			badgeText = "✓ APPROVED"
			badgeStyle = badgeCleanStyle
		case "COMMENTED":
			badgeText = "● COMMENT"
			badgeStyle = badgeBehindStyle
		default:
			badgeText = "● REVIEW"
			badgeStyle = badgeBehindStyle
		}
	case notify.TriggerPRMerged:
		badgeText = "◆ MERGED"
		badgeStyle = badgeQueuedStyle
	case notify.TriggerCIPassed:
		badgeText = "✓ CI PASS"
		badgeStyle = badgeCleanStyle
	default:
		badgeText = "● NOTIF"
		badgeStyle = ciNeutralStyle
	}
	return badgeStyle.Render(badgeText)
}

// humanAge renders a duration as a compact age string like "45s", "3m", "2h".
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FormatCIDuration formats a CI duration with second precision, e.g. "45s", "14m12s", "1h2m3s".
func FormatCIDuration(d time.Duration) string {
	return max(0, d).Truncate(time.Second).String()
}

func renderProgressBar(loaded, total, barWidth int) string {
	if total <= 0 || barWidth <= 0 {
		return ""
	}
	ratio := float64(loaded) / float64(total)
	if ratio < 0 {
		ratio = 0
	} else if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(barWidth))
	empty := barWidth - filled

	var b strings.Builder
	b.WriteString("[")
	if filled > 0 {
		b.WriteString(progressBarFilledStyle.Render(strings.Repeat("█", filled)))
	}
	if empty > 0 {
		b.WriteString(progressBarEmptyStyle.Render(strings.Repeat("░", empty)))
	}
	b.WriteString("]")
	return b.String()
}

func (m Model) renderModal() string {
	scored := m.SelectedScored()
	if scored == nil {
		return ""
	}

	var b strings.Builder

	title := fmt.Sprintf("Score Breakdown: %s", scored.PR.Key())
	if scored.PR.RepoNameWithOwner == "" {
		title = fmt.Sprintf("Score Breakdown: #%d", scored.PR.Number)
	}
	b.WriteString(modalTitleStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(scored.PR.Title))
	b.WriteString("\n")

	if scored.PR.IsPartOfStack() {
		stackInfo := fmt.Sprintf(
			"Stack: #%d • Entry %d of %d",
			scored.PR.Stack.Number,
			scored.PR.Stack.Position,
			scored.PR.Stack.Size,
		)
		if scored.PR.Stack.BaseRefName != "" {
			stackInfo += fmt.Sprintf(" (base: %s)", scored.PR.Stack.BaseRefName)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Render(stackInfo))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Score: %.1f\n\n", scored.Breakdown.Total)

	for _, term := range scored.Breakdown.Terms {
		fmt.Fprintf(
			&b,
			"  %-18s raw=%-6.1f wt=%-6.1f contrib=%+.1f\n",
			term.Name,
			term.Raw,
			term.Weight,
			term.Contribution,
		)
	}

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Faint(true).Render("Press '?' or 'esc' to close"))

	return modalBoxStyle.Render(b.String())
}

func (m Model) renderDetailsModal() string {
	pr := m.SelectedPR()
	if pr == nil {
		return ""
	}

	var b strings.Builder

	repo := pr.RepoNameWithOwner
	if repo == "" {
		if pr.RepoOwner != "" && pr.RepoName != "" {
			repo = pr.RepoOwner + "/" + pr.RepoName
		} else if pr.RepoName != "" {
			repo = pr.RepoName
		}
	}
	title := fmt.Sprintf("PR #%d: %s", pr.Number, pr.Title)
	b.WriteString(modalTitleStyle.Render(title))
	b.WriteString("\n\n")

	// Metadata grid / key-values
	if repo != "" {
		fmt.Fprintf(&b, "%-10s %s\n", faintStyle.Render("Repo:"), repoStyle.Render(repo))
	}
	if pr.Author != "" {
		authorStr := "@" + pr.Author
		if pr.IsAuthorBot() {
			authorStr += " [bot]"
		}
		fmt.Fprintf(&b, "%-10s %s\n", faintStyle.Render("Author:"), authorStr)
	}
	if pr.HeadRefName != "" || pr.BaseRefName != "" {
		branchStr := fmt.Sprintf("%s → %s", pr.HeadRefName, pr.BaseRefName)
		fmt.Fprintf(
			&b,
			"%-10s %s\n",
			faintStyle.Render("Branches:"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Render(branchStr),
		)
	}

	refTime := m.refTime()
	if !pr.UpdatedAt.IsZero() {
		ageStr := humanAge(refTime.Sub(pr.UpdatedAt)) + " ago"
		if !pr.CreatedAt.IsZero() {
			ageStr += fmt.Sprintf(" (created %s ago)", humanAge(refTime.Sub(pr.CreatedAt)))
		}
		fmt.Fprintf(&b, "%-10s %s\n", faintStyle.Render("Updated:"), ageStr)
	}

	statusBadge := renderStatusBadge(*pr)
	mergeStr := pr.Mergeable
	if pr.InMergeQueue() {
		mergeStr = "IN MERGE QUEUE"
	}
	fmt.Fprintf(
		&b,
		"%-10s %s  %s: %s\n",
		faintStyle.Render("Status:"),
		statusBadge,
		faintStyle.Render("Merge:"),
		mergeStr,
	)

	if pr.IsPartOfStack() {
		stackInfo := fmt.Sprintf(
			"Stack #%d • Entry %d of %d",
			pr.Stack.Number,
			pr.Stack.Position,
			pr.Stack.Size,
		)
		if pr.Stack.BaseRefName != "" {
			stackInfo += fmt.Sprintf(" (base: %s)", pr.Stack.BaseRefName)
		}
		fmt.Fprintf(
			&b,
			"%-10s %s\n",
			faintStyle.Render("Stack:"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Render(stackInfo),
		)
	}

	// Reviews
	if len(pr.LatestReviews) > 0 {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Bold(true).Render("Reviews:"))
		b.WriteString("\n")
		for _, rev := range pr.LatestReviews {
			stateStyle := lipgloss.NewStyle()
			var icon string
			switch rev.State {
			case "APPROVED":
				stateStyle = stateStyle.Foreground(lipgloss.Color("42"))
				icon = "✓"
			case "CHANGES_REQUESTED":
				stateStyle = stateStyle.Foreground(lipgloss.Color("196"))
				icon = "✗"
			default:
				stateStyle = stateStyle.Foreground(lipgloss.Color("245"))
				icon = "•"
			}
			age := ""
			if !rev.SubmittedAt.IsZero() {
				age = fmt.Sprintf(" (%s ago)", humanAge(refTime.Sub(rev.SubmittedAt)))
			}
			fmt.Fprintf(&b, "  %s @%s %s%s\n", icon, rev.Author, stateStyle.Render(rev.State), faintStyle.Render(age))
		}
	}

	// CI Checks
	if pr.Checks.Total > 0 || pr.Checks.ReqTotal > 0 {
		b.WriteString("\n")
		ciTitle := "CI Checks: " + m.renderCIBadge(pr.Checks, refTime)
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(ciTitle))
		b.WriteString("\n")
		durationSuffix := ""
		if d, ok := pr.Checks.Duration(refTime); ok {
			if pr.Checks.IsRunning() {
				durationSuffix = " | Elapsed: " + FormatCIDuration(d)
			} else {
				durationSuffix = " | Duration: " + FormatCIDuration(d)
			}
		}
		fmt.Fprintf(&b, "  Total: %d | Done: %d | Failed: %d | Running: %d%s\n",
			pr.Checks.Total, pr.Checks.Done, pr.Checks.Failed, pr.Checks.Running, durationSuffix)
	}

	// Changed Files & Diffstat
	b.WriteString("\n")
	sizeText := renderDiffSize(pr.Additions, pr.Deletions)
	filesTitle := fmt.Sprintf("Files Changed: %s across %d files", sizeText, pr.ChangedFiles)
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(filesTitle))
	b.WriteString("\n")
	if len(pr.Files) > 0 {
		maxFiles := min(8, len(pr.Files))
		for i := range maxFiles {
			fmt.Fprintf(&b, "  %s\n", pr.Files[i])
		}
		if len(pr.Files) > maxFiles {
			fmt.Fprintf(&b, "  %s\n", faintStyle.Render(fmt.Sprintf("… and %d more files", len(pr.Files)-maxFiles)))
		}
	}

	// Shortcuts footer
	b.WriteString("\n")
	b.WriteString(
		lipgloss.NewStyle().
			Faint(true).
			Render("v: gh pr view • o: browser • ?: why score • esc/q/enter: close"),
	)

	return modalBoxStyle.Render(b.String())
}

func (m Model) spinnerChar() string {
	if len(spinnerFrames) == 0 {
		return "⠋"
	}
	return spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
}

func (m Model) renderCIBadge(summary model.ChecksSummary, refTime time.Time) string {
	badge := summary.BadgeWithSpinner(m.spinnerChar())
	if badge == "" {
		return ""
	}
	text := strings.TrimPrefix(badge, "CI: ")
	var renderedBadge string
	switch {
	case summary.IsFailing():
		renderedBadge = ciFailStyle.Render(text)
	case summary.IsRunning():
		renderedBadge = ciRunningStyle.Render(text)
	case summary.IsPassing():
		renderedBadge = ciPassStyle.Render(text)
	default:
		renderedBadge = ciNeutralStyle.Render(text)
	}
	if d, ok := summary.Duration(refTime); ok {
		return renderedBadge + "  " + ciDurationStyle.Render(FormatCIDuration(d))
	}
	return renderedBadge
}

func renderStatusBadge(pr model.PullRequest) string {
	actionStatus := pr.ActionStatus()
	switch actionStatus {
	case model.ActionStatusClean:
		return badgeCleanStyle.Render("● CLEAN")
	case model.ActionStatusQueued:
		return badgeQueuedStyle.Render("◆ QUEUED")
	case model.ActionStatusConflict:
		return badgeConflictStyle.Render("✖ CONFLICT")
	case model.ActionStatusBlocked:
		return badgeBlockedStyle.Render("✖ BLOCKED")
	case model.ActionStatusFailingCI:
		return badgeConflictStyle.Render("✖ FAILING CI")
	case model.ActionStatusChangesReq:
		return badgeBlockedStyle.Render("✖ CHANGES REQ")
	case model.ActionStatusNeedsReview:
		return badgeBehindStyle.Render("● NEEDS REVIEW")
	case model.ActionStatusCIRunning:
		return ciRunningStyle.Render("◐ CI RUNNING")
	case model.ActionStatusBehind:
		return badgeBehindStyle.Render("◷ BEHIND")
	case model.ActionStatusDraft:
		return badgeDraftStyle.Render("○ DRAFT")
	default:
		badge := actionStatus.Badge()
		if badge == "" {
			return renderMergeBadge(pr.MergeStatus)
		}
		return lipgloss.NewStyle().Bold(true).Render(badge)
	}
}

func renderMergeBadge(status model.MergeStatus) string {
	switch status.State() {
	case model.MergeStateClean:
		return badgeCleanStyle.Render("● CLEAN")
	case model.MergeStateQueued:
		return badgeQueuedStyle.Render("◆ QUEUED")
	case model.MergeStateConflict:
		return badgeConflictStyle.Render("✖ CONFLICT")
	case model.MergeStateBlocked:
		return badgeBlockedStyle.Render("✖ BLOCKED")
	case model.MergeStateBehind:
		return badgeBehindStyle.Render("◷ BEHIND")
	case model.MergeStateDraft:
		return badgeDraftStyle.Render("○ DRAFT")
	default:
		badge := status.Badge()
		if badge == "" {
			return ""
		}
		return lipgloss.NewStyle().Bold(true).Render(strings.ToUpper(strings.Trim(badge, "[]")))
	}
}

func renderAuthor(pr model.PullRequest) string {
	if pr.Author == "" {
		return ""
	}
	authorText := authorStyle.Render("@" + pr.Author)
	if pr.IsAuthorBot() {
		return authorText + " " + botBadgeStyle.Render("BOT")
	}
	return authorText
}

func renderDiffSize(additions, deletions int) string {
	add := diffAddStyle.Render("+" + FormatDiff(additions))
	del := diffDelStyle.Render("-" + FormatDiff(deletions))
	return fmt.Sprintf("%s %s", add, del)
}

func renderCategoryHeader(title string, count int, accentColor lipgloss.TerminalColor, width int) string {
	if accentColor == nil {
		accentColor = lipgloss.Color("#58a6ff")
	}
	bar := lipgloss.NewStyle().Foreground(accentColor).Render("▌ ")
	name := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Render(strings.ToUpper(title))

	badge := lipgloss.NewStyle().
		Background(accentColor).
		Foreground(lipgloss.Color("#1E1E2E")).
		Bold(true).
		Padding(0, 1).
		Render(strconv.Itoa(count))

	prefix := fmt.Sprintf("%s%s  %s ", bar, name, badge)
	ruleLen := max(width-lipgloss.Width(prefix), 0)

	rule := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3B4252")).
		Render(strings.Repeat("─", ruleLen))

	return prefix + rule
}

func renderCategoryDivider(text string, width int) string {
	if width > len(text) {
		text += strings.Repeat("─", width-len(text))
	}
	return dividerStyle.Render(text)
}

func renderHelp(text string) string {
	parts := strings.Split(text, " • ")
	styledParts := make([]string, len(parts))
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("250"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	for i, part := range parts {
		if k, d, ok := strings.Cut(part, ": "); ok {
			styledParts[i] = keyStyle.Render(k) + descStyle.Render(": "+d)
		} else {
			styledParts[i] = descStyle.Render(part)
		}
	}
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(" • ")
	return lipgloss.NewStyle().MarginTop(1).Render(strings.Join(styledParts, sep))
}
