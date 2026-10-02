package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/score"
)

// tableProjection represents the visual layout of table rows for a given tab,
// sorting strategy, and focus/stack state.
type tableProjection struct {
	items          []score.Scored
	projectionRows []displayRow
	visibleIndices []int
	// rowOf maps an item index to its item row, or -1 when the item has no
	// row of its own (a hidden member of a collapsed stack, or unsectioned).
	rowOf []int
}

// rows returns the ordered slice of display rows (dividers, banners, items).
func (p tableProjection) rows() []displayRow {
	return p.projectionRows
}

// rowCount returns the total number of display rows.
func (p tableProjection) rowCount() int {
	return len(p.projectionRows)
}

// rowAt returns the display row at display row index idx.
func (p tableProjection) rowAt(idx int) (displayRow, bool) {
	if idx >= 0 && idx < len(p.projectionRows) {
		return p.projectionRows[idx], true
	}
	return displayRow{}, false
}

// visibleItemIndices returns the indices into the source PR list for selectable rows.
func (p tableProjection) visibleItemIndices() []int {
	return p.visibleIndices
}

// displayCursor maps an item index in the source list to the display row that
// represents it. A hidden member of a collapsed stack maps to the stack's
// collapsed row; an item with no representing row maps to 0.
func (p tableProjection) displayCursor(itemIndex int) int {
	if itemIndex < 0 || itemIndex >= len(p.rowOf) {
		return 0
	}
	if r := p.rowOf[itemIndex]; r >= 0 {
		return r
	}
	key := p.items[itemIndex].PR.Key()
	for r, row := range p.projectionRows {
		if row.isCollapsedStack && row.stackGroup.contains(key) {
			return r
		}
	}
	return 0
}

// scrollAnchor returns the display row scrolling should keep visible for the
// cursor at itemIndex. At the list top it anchors to row 0 so the first
// section divider stays visible: ranking sorts by the same categories the
// display groups by, so the first visible item sits at row 0 or 1.
func (p tableProjection) scrollAnchor(itemIndex int) int {
	if len(p.visibleIndices) > 0 && itemIndex == p.visibleIndices[0] {
		return 0
	}
	return p.displayCursor(itemIndex)
}

// collapsedStack returns the stack group if the item at itemIndex is a collapsed stack root.
func (p tableProjection) collapsedStack(itemIndex int) (*StackGroup, bool) {
	if itemIndex < 0 || itemIndex >= len(p.rowOf) || p.rowOf[itemIndex] < 0 {
		return nil, false
	}
	row := p.projectionRows[p.rowOf[itemIndex]]
	if !row.isCollapsedStack || row.stackGroup == nil {
		return nil, false
	}
	return row.stackGroup, true
}

// projectionOptions configures table projection generation.
type projectionOptions struct {
	Tab          Tab
	Items        []score.Scored
	SortStrategy SortStrategy
	RefTime      time.Time
	IsFocused    func(model.PullRequest) bool
	IsExpanded   func(string) bool
	// MineKeys and Viewer identify authored PRs; only the Focus tab under
	// sortGroupsBySection reads them.
	MineKeys map[model.PRKey]bool
	Viewer   string
}

// sortGroupsBySection reports whether s lays rows out under the tab's
// section dividers (as opposed to repo/author groups or a flat order).
func sortGroupsBySection(s SortStrategy) bool {
	switch s {
	case SortRepo, SortAuthor, SortUpdated, SortScore:
		return false
	}
	return true
}

// buildProjection constructs an indexed tableProjection from the given options.
func buildProjection(opts projectionOptions) tableProjection {
	list := opts.Items
	if len(list) == 0 {
		return tableProjection{}
	}

	if opts.IsFocused == nil {
		opts.IsFocused = func(model.PullRequest) bool { return false }
	}
	if opts.IsExpanded == nil {
		opts.IsExpanded = func(string) bool { return false }
	}

	rows := buildRowsForTab(opts)

	visibleIndices := make([]int, 0, len(list))
	rowOf := make([]int, len(list))
	for i := range rowOf {
		rowOf[i] = -1
	}
	for rIdx, r := range rows {
		if r.kind == rowItem {
			visibleIndices = append(visibleIndices, r.itemIndex)
			rowOf[r.itemIndex] = rIdx
		}
	}

	return tableProjection{
		items:          list,
		projectionRows: rows,
		visibleIndices: visibleIndices,
		rowOf:          rowOf,
	}
}

func buildRowsForTab(opts projectionOptions) []displayRow {
	list, isFocused, isExpanded := opts.Items, opts.IsFocused, opts.IsExpanded
	switch opts.SortStrategy {
	case SortRepo:
		return buildRepoDisplayRows(list, isFocused, isExpanded)
	case SortAuthor:
		return buildAuthorDisplayRows(list, isFocused, isExpanded)
	case SortUpdated:
		return buildUpdatedDisplayRows(list, isFocused, isExpanded)
	case SortScore:
		return buildScoreDisplayRows(list, isFocused, isExpanded)
	}
	switch opts.Tab {
	case TabFocus:
		isAuthored := func(pr model.PullRequest) bool {
			return opts.MineKeys[pr.Key()] || (opts.Viewer != "" && strings.EqualFold(pr.Author, opts.Viewer))
		}
		// Every Focus row is focused, so there is nothing to partition.
		return buildSectionedDisplayRows(list, model.AllSections, isAuthored, opts.RefTime, nil, isExpanded)
	case TabPriority, TabInbox:
		notAuthored := func(model.PullRequest) bool { return false }
		return buildSectionedDisplayRows(list, model.InboxSections, notAuthored, opts.RefTime, isFocused, isExpanded)
	case TabMine:
		authored := func(model.PullRequest) bool { return true }
		return buildSectionedDisplayRows(list, model.MineSections, authored, opts.RefTime, isFocused, isExpanded)
	default:
		return nil
	}
}

func partitionFocusedWithStacks(indices []int, list []score.Scored, isFocused func(model.PullRequest) bool) {
	stackFocused := make(map[string]bool)
	for _, idx := range indices {
		pr := list[idx].PR
		if pr.IsPartOfStack() && isFocused(pr) {
			stackFocused[pr.StackKey()] = true
		}
	}

	sort.SliceStable(indices, func(i, j int) bool {
		prI := list[indices[i]].PR
		prJ := list[indices[j]].PR
		iFoc := isFocused(prI) || (prI.IsPartOfStack() && stackFocused[prI.StackKey()])
		jFoc := isFocused(prJ) || (prJ.IsPartOfStack() && stackFocused[prJ.StackKey()])
		return iFoc && !jFoc
	})
}

func buildRepoDisplayRows(
	list []score.Scored,
	isFocused func(model.PullRequest) bool,
	isExpanded func(string) bool,
) []displayRow {
	return buildGroupedDisplayRows(list, prRepoKey, strings.ToUpper, nil, isFocused, isExpanded)
}

func buildAuthorDisplayRows(
	list []score.Scored,
	isFocused func(model.PullRequest) bool,
	isExpanded func(string) bool,
) []displayRow {
	title := func(key string) string {
		if key == "" {
			return "UNKNOWN"
		}
		return "@" + strings.ToUpper(key)
	}
	less := func(a, b string) int {
		if (a == "") != (b == "") {
			if a == "" {
				return 1
			}
			return -1
		}
		return strings.Compare(a, b)
	}
	return buildGroupedDisplayRows(list, prAuthorKey, title, less, isFocused, isExpanded)
}

func buildGroupedDisplayRows(
	list []score.Scored,
	keyFn func(model.PullRequest) string,
	titleFn func(string) string,
	cmp func(a, b string) int,
	isFocused func(model.PullRequest) bool,
	isExpanded func(string) bool,
) []displayRow {
	groups := make(map[string][]int)
	for i, item := range list {
		k := keyFn(item.PR)
		groups[k] = append(groups[k], i)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	if cmp == nil {
		cmp = strings.Compare
	}
	slices.SortFunc(keys, cmp)

	var displayRows []displayRow
	for _, k := range keys {
		indices := groups[k]
		partitionFocusedWithStacks(indices, list, isFocused)

		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  titleFn(k),
			dividerCount:  len(indices),
			dividerAccent: accentBlue,
		})
		displayRows = append(displayRows, buildCategoryDisplayRows(list, indices, isExpanded)...)
	}
	return displayRows
}

// updatedSortKey is the lexicographic key the Updated sort orders by. Stack
// members share every field up to position, so a stack is never split by
// unrelated PRs and the ordering stays a strict weak order.
type updatedSortKey struct {
	focused  bool
	updated  time.Time // latest UpdatedAt across the PR's stack
	groupNum int       // lowest PR number in the PR's stack
	groupID  string    // disambiguates equal numbers across repos/stacks
	position int
	number   int
}

func (a updatedSortKey) less(b updatedSortKey) bool {
	if a.focused != b.focused {
		return a.focused
	}
	if !a.updated.Equal(b.updated) {
		return a.updated.After(b.updated)
	}
	if a.groupNum != b.groupNum {
		return a.groupNum < b.groupNum
	}
	if a.groupID != b.groupID {
		return a.groupID < b.groupID
	}
	if a.position != b.position {
		return a.position < b.position
	}
	return a.number < b.number
}

func buildUpdatedDisplayRows(
	list []score.Scored,
	isFocused func(model.PullRequest) bool,
	isExpanded func(string) bool,
) []displayRow {
	type stackAgg struct {
		updated time.Time
		focused bool
		minNum  int
	}
	stacks := make(map[string]stackAgg)
	for _, item := range list {
		pr := item.PR
		if !pr.IsPartOfStack() {
			continue
		}
		k := pr.StackKey()
		agg, ok := stacks[k]
		if !ok || pr.UpdatedAt.After(agg.updated) {
			agg.updated = pr.UpdatedAt
		}
		if !ok || pr.Number < agg.minNum {
			agg.minNum = pr.Number
		}
		agg.focused = agg.focused || isFocused(pr)
		stacks[k] = agg
	}

	keys := make([]updatedSortKey, len(list))
	for i, item := range list {
		pr := item.PR
		k := updatedSortKey{
			focused:  isFocused(pr),
			updated:  pr.UpdatedAt,
			groupNum: pr.Number,
			groupID:  "pr:" + pr.Key().String(),
			number:   pr.Number,
		}
		if pr.IsPartOfStack() {
			agg := stacks[pr.StackKey()]
			k.focused = k.focused || agg.focused
			k.updated = agg.updated
			k.groupNum = agg.minNum
			k.groupID = "stack:" + pr.StackKey()
			k.position = pr.Stack.Position
		}
		keys[i] = k
	}

	indices := make([]int, len(list))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return keys[indices[i]].less(keys[indices[j]])
	})

	return buildCategoryDisplayRows(list, indices, isExpanded)
}

func buildScoreDisplayRows(
	list []score.Scored,
	isFocused func(model.PullRequest) bool,
	isExpanded func(string) bool,
) []displayRow {
	indices := make([]int, len(list))
	for i := range indices {
		indices[i] = i
	}
	partitionFocusedWithStacks(indices, list, isFocused)
	return buildCategoryDisplayRows(list, indices, isExpanded)
}

// newStackGroup builds the stack group for the members at indices, rooted at root.
func newStackGroup(list []score.Scored, indices []int, root model.PullRequest, expanded bool) *StackGroup {
	prItems := make([]PRItem, len(indices))
	for i, idx := range indices {
		pr := list[idx].PR
		prItems[i] = PRItem{
			Number:    pr.Number,
			Title:     pr.Title,
			Index:     pr.Stack.Position,
			Total:     pr.Stack.Size,
			Additions: pr.Additions,
			Deletions: pr.Deletions,
			Status:    DeterminePRStatus(pr),
			CIStatus:  DeterminePRCIStatus(pr),
			PR:        pr,
		}
	}
	return &StackGroup{
		ID:       root.Number,
		Repo:     root.RepoName,
		Expanded: expanded,
		PRs:      prItems,
	}
}

// contains reports whether the PR identified by key is a member of sg.
func (sg *StackGroup) contains(key model.PRKey) bool {
	if sg == nil {
		return false
	}
	for _, it := range sg.PRs {
		if it.PR.Key() == key {
			return true
		}
	}
	return false
}

func buildCategoryDisplayRows(list []score.Scored, indices []int, isExpanded func(string) bool) []displayRow {
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
			meta := styleRangeTag.Render(fmt.Sprintf("[%d/%d]", pr.Stack.Position, pr.Stack.Size))
			rows = append(rows, displayRow{
				kind:        rowItem,
				itemIndex:   idx,
				stackPrefix: meta,
			})
			continue
		}

		expanded := isExpanded(key)
		if posInCat == 0 {
			sg := newStackGroup(list, inCatIndices, pr, expanded)
			if !expanded {
				rows = append(rows, displayRow{
					kind:             rowItem,
					itemIndex:        idx,
					isCollapsedStack: true,
					stackGroup:       sg,
				})
				continue
			}
			rows = append(rows, displayRow{
				kind:       rowStackBanner,
				itemIndex:  idx,
				stackGroup: sg,
			})
		}
		if !expanded {
			continue
		}

		glyph := "├─ "
		if posInCat == totalInCat-1 {
			glyph = "╰─ "
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

// sectionDivider is how a model.Section renders as a table divider.
type sectionDivider struct {
	title  string
	accent lipgloss.TerminalColor
}

var sectionDividers = map[model.Section]sectionDivider{
	model.SectionAttention:      {"NEEDS YOUR ATTENTION", accentCoral},
	model.SectionActionRequired: {"ACTION REQUIRED", accentCoral},
	model.SectionMergeQueue:     {"MERGE QUEUE", accentViolet},
	model.SectionReadyToMerge:   {"READY TO MERGE", accentGreen},
	model.SectionInReview:       {"IN REVIEW", accentBlue},
	model.SectionBlocked:        {"BLOCKED", accentRed},
	model.SectionDrafts:         {"DRAFTS", accentCharcoal},
	model.SectionStale:          {"STALE", accentCharcoal},
}

// buildSectionedDisplayRows lays list out under one divider per non-empty
// section in order. PRs whose section is not in order are omitted. When
// isFocused is non-nil, focused PRs (and their stacks) lead each section.
func buildSectionedDisplayRows(
	list []score.Scored,
	order []model.Section,
	isAuthored func(model.PullRequest) bool,
	refTime time.Time,
	isFocused func(model.PullRequest) bool,
	isExpanded func(string) bool,
) []displayRow {
	sections := model.EffectiveSections(scoredPRs(list), isAuthored, refTime)

	bySection := make(map[model.Section][]int, len(order))
	for i, item := range list {
		sec := sections[item.PR.Key()]
		bySection[sec] = append(bySection[sec], i)
	}

	displayRows := make([]displayRow, 0, len(list)+len(order))
	for _, sec := range order {
		indices := bySection[sec]
		if len(indices) == 0 {
			continue
		}
		if isFocused != nil {
			partitionFocusedWithStacks(indices, list, isFocused)
		}
		div := sectionDividers[sec]
		displayRows = append(displayRows, displayRow{
			kind:          rowDivider,
			dividerTitle:  div.title,
			dividerCount:  len(indices),
			dividerAccent: div.accent,
		})
		displayRows = append(displayRows, buildCategoryDisplayRows(list, indices, isExpanded)...)
	}
	return displayRows
}

func scoredPRs(list []score.Scored) []model.PullRequest {
	prs := make([]model.PullRequest, len(list))
	for i, item := range list {
		prs[i] = item.PR
	}
	return prs
}
