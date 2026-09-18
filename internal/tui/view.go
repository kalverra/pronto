package tui

import (
	"fmt"
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
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("63")).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Padding(0, 2)

	cursorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("63"))

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("255"))

	unselectedRowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("250"))

	authorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)

	errorBannerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("9"))

	confirmPromptStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("214"))

	successBannerStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#3fb950"))

	notificationFailStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#f85149"))

	notificationPassStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#3fb950"))

	notificationNeutralStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.Color("#58a6ff"))

	notificationMergedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#a371f7"))

	faintStyle = lipgloss.NewStyle().
			Faint(true)

	dividerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("242"))

	modalBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(1, 2)

	modalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))

	progressBarFilledStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("63"))

	progressBarEmptyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("238"))

	tableBorderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240"))

	badgeQueuedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#a371f7")).
				Background(lipgloss.Color("#261b3d")).
				Padding(0, 1)

	badgeCleanStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#3fb950")).
			Background(lipgloss.Color("#13231b")).
			Padding(0, 1)

	badgeBehindStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#e3b341")).
				Background(lipgloss.Color("#2a200e")).
				Padding(0, 1)

	badgeConflictStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#f85149")).
				Background(lipgloss.Color("#2e1518")).
				Padding(0, 1)

	badgeBlockedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#e06c75")).
				Background(lipgloss.Color("#281618")).
				Padding(0, 1)

	badgeDraftStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#8b949e")).
			Background(lipgloss.Color("#1f242c")).
			Padding(0, 1)

	ciPassStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#3fb950")).
			Background(lipgloss.Color("#13231b")).
			Padding(0, 1)

	ciRunningStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#e3b341")).
			Background(lipgloss.Color("#2a200e")).
			Padding(0, 1)

	ciFailStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#f85149")).
			Background(lipgloss.Color("#2e1518")).
			Padding(0, 1)

	ciNeutralStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("245")).
			Background(lipgloss.Color("#1f242c")).
			Padding(0, 1)

	botBadgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#a5b4fc")).
			Background(lipgloss.Color("#1e2038")).
			Padding(0, 1)

	repoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	updatedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	diffAddStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3fb950"))

	diffDelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f85149"))

	numStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("141"))
)

type rowKind int

const (
	rowItem rowKind = iota
	rowDivider
)

type displayRow struct {
	kind        rowKind
	itemIndex   int
	dividerText string
	stackPrefix string
}

func (m Model) buildDisplayRows(tab Tab) []displayRow {
	var list []score.Scored
	if tab == TabInbox {
		list = m.inboxItems
	} else {
		list = m.mineItems
	}
	if len(list) == 0 {
		return nil
	}

	refTime := m.refTime()

	if tab == TabInbox {
		return m.buildInboxDisplayRows(list, refTime)
	}
	return m.buildMineDisplayRows(list, refTime)
}

func (m Model) buildCategoryDisplayRows(list []score.Scored, indices []int) []displayRow {
	if len(indices) == 0 {
		return nil
	}
	stackCounts := make(map[string]int)
	for _, idx := range indices {
		pr := list[idx].PR
		if pr.IsPartOfStack() {
			stackCounts[pr.StackKey()]++
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
		totalInCat := stackCounts[key]
		posInCat := seenInStack[key]
		seenInStack[key]++

		if totalInCat <= 1 {
			prefix := fmt.Sprintf("╶ [%d/%d] ", pr.Stack.Position, pr.Stack.Size)
			rows = append(rows, displayRow{
				kind:        rowItem,
				itemIndex:   idx,
				stackPrefix: prefix,
			})
			continue
		}

		if !m.isStackExpanded(key) {
			if posInCat == 0 {
				hiddenCount := totalInCat - 1
				prefix := fmt.Sprintf("🥞 [%d/%d] (+%d) ", pr.Stack.Position, pr.Stack.Size, hiddenCount)
				rows = append(rows, displayRow{
					kind:        rowItem,
					itemIndex:   idx,
					stackPrefix: prefix,
				})
			}
			continue
		}

		var glyph string
		switch posInCat {
		case 0:
			glyph = "┌ "
		case totalInCat - 1:
			glyph = "└ "
		default:
			glyph = "├ "
		}
		prefix := fmt.Sprintf("%s[%d/%d] ", glyph, pr.Stack.Position, pr.Stack.Size)
		rows = append(rows, displayRow{
			kind:        rowItem,
			itemIndex:   idx,
			stackPrefix: prefix,
		})
	}
	return rows
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

	displayRows := make([]displayRow, 0, len(list)+3)
	if len(attentionIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── NEEDS YOUR ATTENTION (%d) ──", len(attentionIndices)),
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, attentionIndices)...)
	}
	if len(blockedIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── BLOCKED (%d) ──", len(blockedIndices)),
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, blockedIndices)...)
	}
	if len(staleIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── STALE (%d) ──", len(staleIndices)),
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

	displayRows := make([]displayRow, 0, len(list)+6)
	if len(actionIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── ACTION REQUIRED (%d) ──", len(actionIndices)),
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, actionIndices)...)
	}
	if len(queuedIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── MERGE QUEUE (%d) ──", len(queuedIndices)),
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, queuedIndices)...)
	}
	if len(readyIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── READY TO MERGE (%d) ──", len(readyIndices)),
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, readyIndices)...)
	}
	if len(reviewIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── IN REVIEW (%d) ──", len(reviewIndices)),
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, reviewIndices)...)
	}
	if len(draftIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── DRAFTS (%d) ──", len(draftIndices)),
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, draftIndices)...)
	}
	if len(staleIndices) > 0 {
		displayRows = append(displayRows, displayRow{
			kind:        rowDivider,
			dividerText: fmt.Sprintf("── STALE (%d) ──", len(staleIndices)),
		})
		displayRows = append(displayRows, m.buildCategoryDisplayRows(list, staleIndices)...)
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

	// Tabs
	tabInboxTitle := fmt.Sprintf("1: Inbox (%d)", len(m.inboxItems))
	tabMineTitle := fmt.Sprintf("2: Mine (%d)", len(m.mineItems))

	var tabs string
	if m.activeTab == TabInbox {
		tabs = lipgloss.JoinHorizontal(
			lipgloss.Top,
			activeTabStyle.Render(tabInboxTitle),
			inactiveTabStyle.Render(tabMineTitle),
		)
	} else {
		tabs = lipgloss.JoinHorizontal(
			lipgloss.Top,
			inactiveTabStyle.Render(tabInboxTitle),
			activeTabStyle.Render(tabMineTitle),
		)
	}

	b.WriteString(tabs)
	b.WriteString("\n\n")

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

		headers := []string{"", "TITLE", "STATUS", "SIZE", "CI", "UPDATED", "REPO"}
		if m.activeTab != TabMine {
			headers = append(headers, "AUTHOR")
		}

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
			Width(max(20, m.width-4)).
			Wrap(false)

		visibleRows := displayRows[scroll:end]
		for _, dRow := range visibleRows {
			if dRow.kind == rowDivider {
				dividerCols := make([]string, len(headers))
				tbl.Row(dividerCols...)
				continue
			}

			item := list[dRow.itemIndex]
			isSelected := dRow.itemIndex == cursor && !m.IsNotificationFocused()

			prefix := "  "
			if isSelected {
				prefix = cursorStyle.Render("> ")
			}

			stackPrefix := dRow.stackPrefix
			numText := numStyle.Render(fmt.Sprintf("(#%d)", item.PR.Number))
			fullTitle := stackPrefix + item.PR.Title
			var titleText string
			if isSelected {
				titleText = fmt.Sprintf("%s %s", selectedRowStyle.Render(fullTitle), numText)
			} else {
				titleText = fmt.Sprintf("%s %s", unselectedRowStyle.Render(fullTitle), numText)
			}

			statusBadge := renderStatusBadge(item.PR)
			sizeText := renderDiffSize(item.PR.Additions, item.PR.Deletions)
			ciBadge := m.renderCIBadge(item.PR.Checks, refTime)
			if item.PR.Partial {
				// Discovery-only PR: unknown status/CI/diff, so render a
				// loading indicator instead of misleading zero values.
				statusBadge = ciRunningStyle.Render(m.spinnerChar() + " LOADING")
				sizeText = faintStyle.Render("…")
				ciBadge = ""
			}

			var updatedText string
			if !item.PR.UpdatedAt.IsZero() {
				d := max(refTime.Sub(item.PR.UpdatedAt), 0)
				updatedText = updatedStyle.Render(humanAge(d))
			}

			repo := item.PR.RepoName
			if repo == "" {
				repo = item.PR.RepoNameWithOwner
			}
			repoText := repoStyle.Render(repo)

			authorText := renderAuthor(item.PR)

			rowCols := []string{prefix, titleText, statusBadge, sizeText, ciBadge, updatedText, repoText}
			if m.activeTab != TabMine {
				rowCols = append(rowCols, authorText)
			}

			tbl.Row(rowCols...)
		}

		tbl.StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if col == 0 {
				s = s.Padding(0, 0)
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
			tblWidth = max(20, m.width-4)
		}
		for i, dRow := range visibleRows {
			if dRow.kind == rowDivider {
				lineIdx := i + 2
				if lineIdx < len(lines) {
					lines[lineIdx] = renderCategoryDivider(dRow.dividerText, tblWidth)
				}
			}
		}
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n")

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
	b.WriteString(
		helpStyle.Render(
			"enter: details • d: diff • ↑/↓: navigate • space/e: expand • tab: switch • o: open • x: close stale • ?: why score • r: refresh • q: quit",
		),
	)
	b.WriteString("\n")

	return appStyle.Render(b.String())
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
	case m.lastNotification != nil:
		return m.renderNotificationBanner()
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
	default:
		return ""
	}
}

func (m Model) renderNotificationBanner() string {
	n := m.lastNotification
	if n == nil {
		return ""
	}
	title := strings.TrimPrefix(n.Title, "PRonto: ")
	content := fmt.Sprintf("★ %s: %s", title, n.Message)
	if m.IsNotificationFocused() {
		content = "> " + content
	}

	var style lipgloss.Style
	switch n.Trigger {
	case notify.TriggerCIFailed, notify.TriggerConflict:
		style = notificationFailStyle
	case notify.TriggerReviewReceived:
		switch n.ReviewState {
		case "CHANGES_REQUESTED":
			style = notificationFailStyle
		case "APPROVED":
			style = notificationPassStyle
		default:
			style = notificationNeutralStyle
		}
	case notify.TriggerPRMerged:
		style = notificationMergedStyle
	case notify.TriggerCIPassed:
		style = notificationPassStyle
	default:
		style = notificationNeutralStyle
	}

	return style.Render(content)
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
				durationSuffix = " | Elapsed: " + humanAge(d)
			} else {
				durationSuffix = " | Duration: " + humanAge(d)
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
			Render("d: diff • v: gh pr view • o: browser • ?: why score • esc/q/enter: close"),
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
	if d, ok := summary.Duration(refTime); ok {
		text += " " + humanAge(d)
	}
	if summary.IsFailing() {
		return ciFailStyle.Render(text)
	}
	if summary.IsRunning() {
		return ciRunningStyle.Render(text)
	}
	if summary.IsPassing() {
		return ciPassStyle.Render(text)
	}
	return ciNeutralStyle.Render(text)
}

func renderStatusBadge(pr model.PullRequest) string {
	actionStatus := pr.ActionStatus()
	switch actionStatus {
	case model.ActionStatusClean:
		return badgeCleanStyle.Render("CLEAN")
	case model.ActionStatusQueued:
		return badgeQueuedStyle.Render("QUEUED")
	case model.ActionStatusConflict:
		return badgeConflictStyle.Render("CONFLICT")
	case model.ActionStatusBlocked:
		return badgeBlockedStyle.Render("BLOCKED")
	case model.ActionStatusFailingCI:
		return badgeBlockedStyle.Render("FAILING CI")
	case model.ActionStatusChangesReq:
		return badgeBlockedStyle.Render("CHANGES REQ")
	case model.ActionStatusNeedsReview:
		return badgeBehindStyle.Render("NEEDS REVIEW")
	case model.ActionStatusCIRunning:
		return ciRunningStyle.Render("CI RUNNING")
	case model.ActionStatusBehind:
		return badgeBehindStyle.Render("BEHIND")
	case model.ActionStatusDraft:
		return badgeDraftStyle.Render("DRAFT")
	default:
		badge := actionStatus.Badge()
		if badge == "" {
			return renderMergeBadge(pr.MergeStatus)
		}
		return lipgloss.NewStyle().Bold(true).Padding(0, 1).Render(badge)
	}
}

func renderMergeBadge(status model.MergeStatus) string {
	switch status.State() {
	case model.MergeStateClean:
		return badgeCleanStyle.Render("CLEAN")
	case model.MergeStateQueued:
		return badgeQueuedStyle.Render("QUEUED")
	case model.MergeStateConflict:
		return badgeConflictStyle.Render("CONFLICT")
	case model.MergeStateBlocked:
		return badgeBlockedStyle.Render("BLOCKED")
	case model.MergeStateBehind:
		return badgeBehindStyle.Render("BEHIND")
	case model.MergeStateDraft:
		return badgeDraftStyle.Render("DRAFT")
	default:
		badge := status.Badge()
		if badge == "" {
			return ""
		}
		return lipgloss.NewStyle().Bold(true).Padding(0, 1).Render(strings.ToUpper(strings.Trim(badge, "[]")))
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
	add := diffAddStyle.Render(fmt.Sprintf("+%d", additions))
	del := diffDelStyle.Render(fmt.Sprintf("-%d", deletions))
	return fmt.Sprintf("%s %s", add, del)
}

func renderCategoryDivider(text string, width int) string {
	if width > len(text) {
		text += strings.Repeat("─", width-len(text))
	}
	return dividerStyle.Render(text)
}
