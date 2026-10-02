package tui

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/muesli/termenv"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/score"
)

var (
	appStyle = lipgloss.NewStyle().Margin(1, 2)

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#58a6ff"))

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8b949e"))

	tabSeparatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("238"))

	cursorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#58a6ff"))

	focusStarStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#e3b341"))

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#ffffff"))

	unselectedRowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#e6edf3"))

	selectedNumStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#79c0ff"))

	selectedRangeTagStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#e6edf3"))

	selectedUpdatedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#e6edf3"))

	selectedRepoStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#e6edf3"))

	selectedAuthorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#ffffff"))

	selectedDiffAddStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#3fb950"))

	selectedDiffDelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#f85149"))

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
			Foreground(lipgloss.Color("#b1bac4"))

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
	selectedRowBg  = lipgloss.Color("#21262d")
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

func (m Model) projection(tab Tab) tableProjection {
	var mineKeys map[model.PRKey]bool
	if tab == TabFocus && sortGroupsBySection(m.sortStrategy) {
		mineKeys = make(map[model.PRKey]bool, len(m.mineItems))
		for _, it := range m.mineItems {
			mineKeys[it.PR.Key()] = true
		}
	}
	return buildProjection(projectionOptions{
		Tab:          tab,
		Items:        m.items(tab),
		SortStrategy: m.sortStrategy,
		RefTime:      m.refTime(),
		IsFocused:    m.isFocused,
		IsExpanded:   m.isStackExpanded,
		MineKeys:     mineKeys,
		Viewer:       m.viewer,
	})
}

func (m Model) isFocused(pr model.PullRequest) bool {
	k := pr.Key()
	if m.focusedKeys[k] {
		return true
	}
	if m.manualUnfocused[k] {
		return false
	}
	return m.focusCfg.Matches(pr)
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
		spin := lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Render(m.spinnerChar())
		if m.loadingTotal > 0 {
			fmt.Fprintf(
				&b,
				"  %s Fetching pull requests (%d of %d hydrated)…\n\n",
				spin,
				m.loadingLoaded,
				m.loadingTotal,
			)
		} else {
			fmt.Fprintf(&b, "  %s Fetching pull requests…\n\n", spin)
		}
	case len(list) == 0:
		b.WriteString("  No pull requests in this view.\n\n")
	default:
		visRows := m.VisibleRows()
		cursor := m.Cursor()

		refTime := m.refTime()

		displayRows := m.projection(m.activeTab).rows()

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
	helpText := "enter: details • ↑/↓: navigate • s: sort • space/e: expand • tab: switch • o: open • f: focus • x: close stale • n: notifs • ?: why • r: refresh • q: quit"
	if m.loading {
		helpText = "q: quit • ?: help"
	} else if m.IsNotificationFocused() {
		helpText = "enter/o: open • ↑/↓: select • x: dismiss • esc: back to PRs • q: quit"
	}
	b.WriteString(renderHelp(helpText))

	// No trailing newline: appStyle's bottom margin already ends the view, and
	// one extra line overflows the terminal (the renderer then cuts the top,
	// hiding the tab bar and shifting mouse coordinates).
	return appStyle.Render(b.String())
}

func computeMaxNumAndStackWidth(displayRows []displayRow, list []score.Scored) (int, int) {
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
			stackMeta := formatCollapsedStackMeta(pr, false)
			if w := lipgloss.Width(stackMeta); w > maxStackWidth {
				maxStackWidth = w
			}
		} else if dRow.stackPrefix != "" {
			if w := lipgloss.Width(dRow.stackPrefix); w > maxStackWidth {
				maxStackWidth = w
			}
		}
	}
	return maxNumWidth, maxStackWidth
}

func (m Model) buildTableRowCols(
	dRow displayRow,
	item score.Scored,
	isSelected, hasAuthor bool,
	refTime time.Time,
	maxNumWidth, maxStackWidth int,
) []string {
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
		rowCols = append(rowCols, renderAuthor(item.PR, isSelected))
	}
	return rowCols
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
	maxNumWidth, maxStackWidth := computeMaxNumAndStackWidth(displayRows, list)

	var rows [][]string
	for _, dRow := range visibleRows {
		if dRow.kind == rowDivider || dRow.kind == rowStackBanner {
			rows = append(rows, make([]string, len(headers)))
			continue
		}

		item := list[dRow.itemIndex]
		isSelected := dRow.itemIndex == cursor && !m.IsNotificationFocused()
		rows = append(rows, m.buildTableRowCols(dRow, item, isSelected, hasAuthor, refTime, maxNumWidth, maxStackWidth))
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
		if row >= 0 && row < len(visibleRows) {
			dRow := visibleRows[row]
			if dRow.kind == rowItem && dRow.itemIndex == cursor && !m.IsNotificationFocused() {
				s = s.Background(selectedRowBg)
			}
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
		cursor:  4,
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
// dividers and expanded stack banners with their full-width rendered forms,
// and ensures seamless background highlight for the selected row.
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
			banner := renderStackBanner(rootPR, dRow.stackGroup.PRs, bannerSelected, tblWidth)
			if bannerSelected {
				banner = applyRowBackground(banner, selectedRowBg)
			}
			lines[lineIdx] = banner
		case dRow.kind == rowItem:
			if dRow.itemIndex == cursor && !m.IsNotificationFocused() {
				lines[lineIdx] = applyRowBackground(lines[lineIdx], selectedRowBg)
			}
		}
	}
}

// applyRowBackground ensures that a row highlighted with bg does not lose its background
// when inner ANSI styling sequences reset character attributes with \x1b[0m.
func applyRowBackground(line string, bg lipgloss.TerminalColor) string {
	sample := lipgloss.NewStyle().Background(bg).Render(" ")
	idx := strings.Index(sample, " ")
	var bgSeq string
	if idx > 0 {
		bgSeq = sample[:idx]
	} else {
		// When default renderer is in Ascii mode (e.g. tests), construct escape sequence via TrueColor renderer
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(termenv.TrueColor)
		s := r.NewStyle().Background(bg).Render(" ")
		if i := strings.Index(s, " "); i > 0 {
			bgSeq = s[:i]
		}
	}
	if bgSeq == "" {
		return line
	}

	// Strip any trailing reset sequences so we don't end with a background sequence before EOL
	trimmed := line
	for {
		if after, ok := strings.CutSuffix(trimmed, "\x1b[0m"); ok {
			trimmed = after
			continue
		}
		if after, ok := strings.CutSuffix(trimmed, "\x1b[m"); ok {
			trimmed = after
			continue
		}
		break
	}

	// Normalize any resets that are already followed by bgSeq
	trimmed = strings.ReplaceAll(trimmed, "\x1b[0m"+bgSeq, "\x1b[0m")
	trimmed = strings.ReplaceAll(trimmed, "\x1b[m"+bgSeq, "\x1b[m")
	trimmed = strings.ReplaceAll(trimmed, "\x1b[49m", bgSeq)

	// Re-apply bgSeq after every reset within the line
	trimmed = strings.ReplaceAll(trimmed, "\x1b[0m", "\x1b[0m"+bgSeq)
	trimmed = strings.ReplaceAll(trimmed, "\x1b[m", "\x1b[m"+bgSeq)

	if !strings.HasPrefix(trimmed, bgSeq) {
		trimmed = bgSeq + trimmed
	}

	// Always close with reset at end of line
	return trimmed + "\x1b[0m"
}

func formatCollapsedStackMeta(pr model.PullRequest, isSelected bool) string {
	if pr.Stack == nil {
		return ""
	}
	rangeStyle := styleRangeTag
	if isSelected {
		rangeStyle = selectedRangeTagStyle
	}
	return rangeStyle.Render(fmt.Sprintf("[%d/%d]", pr.Stack.Position, pr.Stack.Size))
}

func renderRowPrefix(isSelected, isFocused, isCollapsedStack bool) string {
	var char0 string
	if isSelected {
		char0 = cursorStyle.Render("❯")
	} else {
		char0 = " "
	}

	var char1 string
	if isFocused {
		char1 = focusStarStyle.Render("★")
	} else {
		char1 = " "
	}

	var char3 string
	if isCollapsedStack {
		if isSelected {
			char3 = cursorStyle.Render("▸")
		} else {
			char3 = faintStyle.Render("▸")
		}
	} else {
		char3 = " "
	}

	return char0 + char1 + " " + char3
}

func (m Model) isStackGroupFocused(sg *StackGroup) bool {
	if sg == nil {
		return false
	}
	for _, it := range sg.PRs {
		if m.isFocused(it.PR) {
			return true
		}
	}
	return false
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
	prefix := renderRowPrefix(isSelected, m.isFocused(root) || m.isStackGroupFocused(sg), true)

	totalAdds, totalDels := 0, 0
	for _, prItem := range sg.PRs {
		totalAdds += prItem.Additions
		totalDels += prItem.Deletions
	}

	stackMeta := formatCollapsedStackMeta(root, isSelected)
	padStack := ""
	if maxStackWidth > lipgloss.Width(stackMeta) {
		padStack = strings.Repeat(" ", maxStackWidth-lipgloss.Width(stackMeta))
	}
	stackPart := stackMeta + padStack

	rootTitle := root.Title

	numStr := fmt.Sprintf("#%d", root.Number)
	padNum := ""
	if maxNumWidth > len(numStr) {
		padNum = strings.Repeat(" ", maxNumWidth-len(numStr))
	}
	numSt := numStyle
	if isSelected {
		numSt = selectedNumStyle
	}
	numText := numSt.Render(numStr) + padNum

	titleStyle := unselectedRowStyle
	if isSelected {
		titleStyle = selectedRowStyle
	}
	titleText := fmt.Sprintf("%s  %s  %s", numText, stackPart, titleStyle.Render(rootTitle))

	var updatedText string
	if !root.UpdatedAt.IsZero() {
		d := max(refTime.Sub(root.UpdatedAt), 0)
		upStyle := updatedStyle
		if isSelected {
			upStyle = selectedUpdatedStyle
		}
		updatedText = upStyle.Render(humanAge(d))
	}

	repo := root.RepoName
	if repo == "" {
		repo = root.RepoNameWithOwner
	}
	rpStyle := repoStyle
	if isSelected {
		rpStyle = selectedRepoStyle
	}

	return []string{
		prefix,
		titleText,
		RenderMicroStatusRibbon(sg.PRs),
		renderDiffSize(totalAdds, totalDels, isSelected),
		RenderMicroCIRibbon(sg.PRs),
		updatedText,
		rpStyle.Render(repo),
	}
}

// childStackRowCols renders a single PR row inside an expanded stack.
func (m Model) childStackRowCols(dRow displayRow, pr model.PullRequest, isSelected bool, refTime time.Time) []string {
	childSelected := isSelected && !dRow.isStackRoot
	prefix := renderRowPrefix(childSelected, m.isFocused(pr), false)

	title := pr.Title
	fullTitle := dRow.stackPrefix + title
	titleStyle := unselectedRowStyle
	if childSelected {
		titleStyle = selectedRowStyle
	}
	titleText := titleStyle.Render(fullTitle)

	var updatedText string
	if !pr.UpdatedAt.IsZero() {
		d := max(refTime.Sub(pr.UpdatedAt), 0)
		upStyle := updatedStyle
		if childSelected {
			upStyle = selectedUpdatedStyle
		}
		updatedText = upStyle.Render(humanAge(d))
	}

	repo := pr.RepoName
	if repo == "" {
		repo = pr.RepoNameWithOwner
	}
	rpStyle := repoStyle
	if childSelected {
		rpStyle = selectedRepoStyle
	}

	return []string{
		prefix,
		titleText,
		RenderChildStatusBadge(pr),
		renderDiffSize(pr.Additions, pr.Deletions, childSelected),
		m.renderCIBadge(pr.DisplayChecks(), refTime, childSelected),
		updatedText,
		rpStyle.Render(repo),
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
	prefix := renderRowPrefix(isSelected, m.isFocused(pr), false)

	numStr := fmt.Sprintf("#%d", pr.Number)
	padNum := ""
	if maxNumWidth > len(numStr) {
		padNum = strings.Repeat(" ", maxNumWidth-len(numStr))
	}
	numSt := numStyle
	if isSelected {
		numSt = selectedNumStyle
	}
	numText := numSt.Render(numStr) + padNum

	title := pr.Title
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
	sizeText := renderDiffSize(pr.Additions, pr.Deletions, isSelected)
	ciBadge := m.renderCIBadge(pr.DisplayChecks(), refTime, isSelected)
	if pr.Partial {
		statusBadge = ciRunningStyle.Render(m.spinnerChar() + " LOADING")
		sizeText = faintStyle.Render("…")
		ciBadge = ""
	}

	var updatedText string
	if !pr.UpdatedAt.IsZero() {
		d := max(refTime.Sub(pr.UpdatedAt), 0)
		upStyle := updatedStyle
		if isSelected {
			upStyle = selectedUpdatedStyle
		}
		updatedText = upStyle.Render(humanAge(d))
	}

	repo := pr.RepoName
	if repo == "" {
		repo = pr.RepoNameWithOwner
	}
	rpStyle := repoStyle
	if isSelected {
		rpStyle = selectedRepoStyle
	}

	return []string{
		prefix,
		titleText,
		statusBadge,
		sizeText,
		ciBadge,
		updatedText,
		rpStyle.Render(repo),
	}
}

// tabTitle returns the tab bar label for tab, e.g. "3: Priority (2)".
func (m Model) tabTitle(tab Tab) string {
	names := map[Tab]string{TabFocus: "Focus", TabMine: "Mine", TabPriority: "Priority", TabInbox: "Inbox"}
	return fmt.Sprintf("%d: %s (%d)", slices.Index(allTabs, tab)+1, names[tab], len(m.items(tab)))
}

// tabGapWidth is the cell width between tab labels ("  |  "); tabAt relies on it.
const tabGapWidth = 5

func (m Model) renderTabBar(contentWidth int) string {
	sep := tabSeparatorStyle.Render("|")
	rendered := make([]string, len(allTabs))
	for i, t := range allTabs {
		if m.activeTab == t {
			rendered[i] = activeTabStyle.Render(m.tabTitle(t))
		} else {
			rendered[i] = inactiveTabStyle.Render(m.tabTitle(t))
		}
	}
	tabs := strings.Join(rendered, "  "+sep+"  ")

	var refreshBadge string
	if m.isRefreshingVisual() {
		spin := lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Render(m.spinnerChar())
		if m.loadingTotal > 0 && m.loadingLoaded < m.loadingTotal {
			refreshBadge = spin + " " + faintStyle.Render(
				fmt.Sprintf("refreshing (%d/%d)", m.loadingLoaded, m.loadingTotal),
			)
		} else {
			refreshBadge = spin + " " + faintStyle.Render("refreshing…")
		}
	} else if m.loading {
		spin := lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Render(m.spinnerChar())
		if m.loadingTotal > 0 && m.loadingLoaded < m.loadingTotal {
			refreshBadge = spin + " " + faintStyle.Render(
				fmt.Sprintf("loading (%d/%d)", m.loadingLoaded, m.loadingTotal),
			)
		} else {
			refreshBadge = spin + " " + faintStyle.Render("loading…")
		}
	}

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

	sortBadge := faintStyle.Render(
		"sort: ",
	) + lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#a371f7")).
		Render(m.sortStrategy.String())
	rightHelp := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("[?] Help")

	var rightElements []string
	if refreshBadge != "" {
		rightElements = append(rightElements, refreshBadge)
	}
	rightElements = append(rightElements, notifBadge, sortBadge, rightHelp)
	rightPart := strings.Join(rightElements, "  "+sep+"  ")

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
	case m.notifyHint() != "":
		return faintStyle.Render(m.notifyHint())
	case m.updateHint() != "":
		return faintStyle.Render(m.updateHint())
	default:
		return ""
	}
}

// updateHint reports a short reminder when a newer pronto release is
// available, or "" when up to date, not checked, or checking is disabled.
func (m Model) updateHint() string {
	if !m.updateInfo.Available {
		return ""
	}
	running := m.version
	if running != "" && !strings.HasPrefix(running, "v") {
		running = "v" + running
	}
	return fmt.Sprintf("🆕 pronto %s available (running %s) — %s", m.updateInfo.Latest, running, m.updateInfo.URL)
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
		isSelected := m.IsNotificationFocused() && i == m.notificationCursor
		prefix := "  "
		if isSelected {
			prefix = cursorStyle.Render("❯ ")
		}

		badge := notificationBadge(n)
		ref, details := formatNotificationContent(n)

		age := "0s"
		if !n.SubmittedAt.IsZero() {
			d := max(m.currentTime().Sub(n.SubmittedAt), 0)
			age = humanAge(d)
		}

		tsStyle := faintStyle
		if isSelected {
			tsStyle = selectedUpdatedStyle
		}
		ts := tsStyle.Render("· " + age + " ago")

		var rowParts []string
		if ref != "" {
			rpStyle := repoStyle
			if isSelected {
				rpStyle = selectedRepoStyle
			}
			rowParts = append(rowParts, rpStyle.Render(ref))
		}
		if details != "" {
			dStyle := unselectedRowStyle
			if isSelected {
				dStyle = selectedRowStyle
			}
			rowParts = append(rowParts, dStyle.Render(details))
		}
		rowParts = append(rowParts, ts)

		content := strings.Join(rowParts, "  ")
		innerContent := fmt.Sprintf("%s%s  %s", prefix, badge, content)

		padLen := max(innerWidth-lipgloss.Width(innerContent), 0)

		innerRow := " " + innerContent + strings.Repeat(" ", padLen) + " "
		if isSelected {
			innerRow = applyRowBackground(innerRow, selectedRowBg)
		}

		rowLine := borderStyle.Render("│") + innerRow + borderStyle.Render("│")
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

	// Category Explanation
	exp := m.ExplainPR(*scored)
	categoryHeader := exp.TabName
	if exp.SectionName != "" {
		categoryHeader += " › " + exp.SectionName
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Category: " + categoryHeader))
	b.WriteString("\n")
	if exp.TabReason != "" {
		fmt.Fprintf(&b, "  • %s: %s\n", exp.TabName, exp.TabReason)
	}
	if exp.BaseTabName != "" && exp.BaseTabReason != "" {
		fmt.Fprintf(&b, "  • Base: %s (%s)\n", exp.BaseTabName, exp.BaseTabReason)
	}
	if exp.SectionReason != "" {
		fmt.Fprintf(&b, "  • %s: %s\n", exp.SectionName, exp.SectionReason)
	}
	if exp.FocusReason != "" {
		fmt.Fprintf(&b, "  • %s\n", exp.FocusReason)
	}
	b.WriteString("\n")

	// Notifications
	notifHeader := "Notifications:"
	if exp.PopupsOff {
		notifHeader += " (desktop popups disabled in config)"
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(notifHeader))
	b.WriteString("\n")

	greenCheck := lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950")).Render("✓")
	redCross := lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")).Render("✗")

	if len(exp.WillNotify) > 0 {
		fmt.Fprintf(&b, "  Will notify (%d):\n", len(exp.WillNotify))
		var items []string
		for _, t := range exp.WillNotify {
			items = append(items, greenCheck+" "+string(t))
		}
		b.WriteString(formatTriggerList(items, 72, "    ") + "\n")
	} else {
		b.WriteString("  Will notify: (none)\n")
	}

	if len(exp.WontNotify) > 0 {
		fmt.Fprintf(&b, "  Won't notify (%d):\n", len(exp.WontNotify))
		var items []string
		for _, t := range exp.WontNotify {
			items = append(items, redCross+" "+string(t))
		}
		b.WriteString(formatTriggerList(items, 72, "    ") + "\n")
	} else {
		b.WriteString("  Won't notify: (none)\n")
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

func formatTriggerList(items []string, maxWidth int, indent string) string {
	var lines []string
	var curLine string
	for _, item := range items {
		itemLen := lipgloss.Width(item)
		switch {
		case curLine == "":
			curLine = indent + item
		case lipgloss.Width(curLine)+2+itemLen > maxWidth:
			lines = append(lines, curLine)
			curLine = indent + item
		default:
			curLine += "  " + item
		}
	}
	if curLine != "" {
		lines = append(lines, curLine)
	}
	return strings.Join(lines, "\n")
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
		if pr.MergeQueue != nil && pr.MergeQueue.Position > 0 {
			mergeStr = fmt.Sprintf("IN MERGE QUEUE (#%d)", pr.MergeQueue.Position)
		}
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
	if reviewsStr := renderDetailsReviews(refTime, pr.LatestReviews); reviewsStr != "" {
		b.WriteString("\n")
		b.WriteString(reviewsStr)
	}

	// CI Checks
	displayChecks := pr.DisplayChecks()
	if displayChecks.Total > 0 || displayChecks.ReqTotal > 0 {
		b.WriteString("\n")
		ciLabel := "CI Checks"
		if pr.InMergeQueue() && displayChecks == pr.MergeQueueChecks {
			ciLabel = "CI Checks (Merge Queue)"
		}
		ciTitle := ciLabel + ": " + m.renderCIBadge(displayChecks, refTime, false)
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(ciTitle))
		b.WriteString("\n")
		durationSuffix := ""
		if d, ok := displayChecks.Duration(refTime); ok {
			if displayChecks.IsRunning() {
				durationSuffix = " | Elapsed: " + FormatCIDuration(d)
			} else {
				durationSuffix = " | Duration: " + FormatCIDuration(d)
			}
		}
		if displayChecks.HasRequiredChecks {
			fmt.Fprintf(
				&b,
				"  Required: %d | Done: %d | Failed: %d | Running: %d%s\n",
				displayChecks.ReqTotal,
				displayChecks.ReqDone,
				displayChecks.ReqFailed,
				displayChecks.ReqRunning,
				durationSuffix,
			)
		} else {
			fmt.Fprintf(&b, "  Total: %d | Done: %d | Failed: %d | Running: %d%s\n",
				displayChecks.Total, displayChecks.Done, displayChecks.Failed, displayChecks.Running, durationSuffix)
		}
	}

	// Changed Files & Diffstat
	b.WriteString("\n")
	sizeText := renderDiffSize(pr.Additions, pr.Deletions, false)
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
			Render("v: gh pr view • o: browser • ?: why • esc/q/enter: close"),
	)

	return modalBoxStyle.Render(b.String())
}

func renderDetailsReviews(refTime time.Time, reviews []model.Review) string {
	if len(reviews) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Reviews:"))
	b.WriteString("\n")
	for _, rev := range reviews {
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
	return b.String()
}

func (m Model) spinnerChar() string {
	if len(spinnerFrames) == 0 {
		return "⠋"
	}
	return spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
}

func formatColoredCIBadge(c model.ChecksSummary, spinner string) string {
	if c.HasRequiredChecks {
		succeeded := c.Succeeded()
		if c.ReqFailed > 0 {
			var parts []string
			if succeeded > 0 {
				parts = append(parts, ciPassStyle.Render(fmt.Sprintf("✓ %d", succeeded)))
				parts = append(parts, ciFailStyle.Render(fmt.Sprintf("✗ %d req", c.ReqFailed)))
			} else {
				parts = append(parts, ciFailStyle.Render(fmt.Sprintf("✗ %d req failed", c.ReqFailed)))
			}
			if c.ReqRunning > 0 {
				parts = append(parts, ciRunningStyle.Render(fmt.Sprintf("(%d %s)", c.ReqRunning, spinner)))
			}
			return strings.Join(parts, "  ")
		}
		if c.ReqRunning > 0 {
			return ciRunningStyle.Render(fmt.Sprintf("%d/%d req (%d %s)", c.ReqDone, c.ReqTotal, c.ReqRunning, spinner))
		}
		if c.ReqTotal > 0 && c.ReqDone == c.ReqTotal {
			return ciPassStyle.Render(fmt.Sprintf("✓ %d/%d", c.ReqDone, c.ReqTotal))
		}
		return ciNeutralStyle.Render(fmt.Sprintf("%d/%d req", c.ReqDone, c.ReqTotal))
	}

	if c.Total == 0 {
		return ""
	}
	succeeded := c.Succeeded()
	if c.Failed > 0 {
		var parts []string
		if succeeded > 0 {
			parts = append(parts, ciPassStyle.Render(fmt.Sprintf("✓ %d", succeeded)))
			parts = append(parts, ciFailStyle.Render(fmt.Sprintf("✗ %d", c.Failed)))
		} else {
			parts = append(parts, ciFailStyle.Render(fmt.Sprintf("✗ %d failed", c.Failed)))
		}
		if c.Running > 0 {
			parts = append(parts, ciRunningStyle.Render(fmt.Sprintf("(%d %s)", c.Running, spinner)))
		}
		return strings.Join(parts, "  ")
	}
	if c.Running > 0 {
		return ciRunningStyle.Render(fmt.Sprintf("%d/%d (%d %s)", c.Done, c.Total, c.Running, spinner))
	}
	if c.Done == c.Total {
		return ciPassStyle.Render(fmt.Sprintf("✓ %d/%d", c.Done, c.Total))
	}
	return ciNeutralStyle.Render(fmt.Sprintf("%d/%d", c.Done, c.Total))
}

func (m Model) renderCIBadge(summary model.ChecksSummary, refTime time.Time, isSelected bool) string {
	badge := formatColoredCIBadge(summary, m.spinnerChar())
	if badge == "" {
		return ""
	}
	if d, ok := summary.Duration(refTime); ok {
		durStyle := ciDurationStyle
		if isSelected {
			durStyle = selectedUpdatedStyle
		}
		return badge + "  " + durStyle.Render(FormatCIDuration(d))
	}
	return badge
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

func renderAuthor(pr model.PullRequest, isSelected bool) string {
	if pr.Author == "" {
		return ""
	}
	style := authorStyle
	if isSelected {
		style = selectedAuthorStyle
	}
	authorText := style.Render("@" + pr.Author)
	if pr.IsAuthorBot() {
		return authorText + " " + botBadgeStyle.Render("BOT")
	}
	return authorText
}

func renderDiffSize(additions, deletions int, isSelected bool) string {
	addStyle := diffAddStyle
	delStyle := diffDelStyle
	if isSelected {
		addStyle = selectedDiffAddStyle
		delStyle = selectedDiffDelStyle
	}
	add := addStyle.Render("+" + FormatDiff(additions))
	del := delStyle.Render("-" + FormatDiff(deletions))
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
