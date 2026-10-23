package tui

import (
	"cmp"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/tui/theme"
)

// Tone is a semantic row color resolved against the current theme.
type Tone int

const (
	ToneFg Tone = iota
	ToneOK
	ToneWarn
	ToneErr
	ToneDim
	ToneAccent
)

// Color is the tone in the current theme.
func (t Tone) Color() tcell.Color {
	switch t {
	case ToneOK:
		return theme.OK
	case ToneWarn:
		return theme.Warn
	case ToneErr:
		return theme.Err
	case ToneDim:
		return theme.Dim
	case ToneAccent:
		return theme.Accent
	}
	return theme.Fg
}

// Row is one table line: rendered cells plus the source object. Tones are
// resolved against the theme when drawn; Color and Colors are fixed colors
// (0 = none) for rows that are not tied to the theme.
type Row struct {
	Cells []string
	Tone  Tone
	// CellTones overrides the row color for single columns.
	CellTones map[int]Tone
	Color     tcell.Color
	Colors    []tcell.Color // per cell, 0 = row color
	// Markup marks the cells whose text is tview markup, colors inside the
	// cell ("[red]SEC[-] [yellow]BRK[-]"); width, filter and sort go by the
	// text without the tags. Other cells are escaped.
	Markup map[int]bool
	// Sort is the sort value of a column whose text is not a plain number
	// ("17k +18", "▄▆█ +79%"); plain numbers sort as numbers by themselves.
	Sort map[int]float64
	Data any
	Key  string // stable identity, keeps the cursor across reloads
}

// text is cell ci as shown, without markup tags.
func (r *Row) text(ci int) string {
	c := cell(r.Cells, ci)
	if r.Markup[ci] {
		return StripTags(c)
	}
	return c
}

// color is the color of cell ci: cell tone, cell color, row tone, row color.
func (r *Row) color(ci int) tcell.Color {
	if t, ok := r.CellTones[ci]; ok {
		return t.Color()
	}
	if ci < len(r.Colors) && r.Colors[ci] != 0 {
		return r.Colors[ci]
	}
	if r.Tone != ToneFg || r.Color == 0 {
		return r.Tone.Color()
	}
	return r.Color
}

// TableView is a list with vim navigation, live filter, sort and marks.
type TableView struct {
	*tview.Table
	app     *App
	title   string
	headers []string
	rows    []Row
	visible []int
	filter  string
	filterR *regexp.Regexp
	sortCol int
	sortAsc bool
	marked  map[string]bool
	loading bool
	extra   string // extra title info
	// width is what the columns need, wide is whether that is more than the
	// view has: then h/l scroll sideways and the hints say so
	width int
	wide  bool

	onEnter  func(r *Row)
	onSelect func(r *Row)
	keys     map[rune]func()
	ctrlKeys map[tcell.Key]func()
}

// maxCellWidth caps a column: longer text is cut, enter shows it in full.
const maxCellWidth = 60

// NewTableView builds an empty table.
func NewTableView(app *App, title string, headers []string) *TableView {
	t := &TableView{
		Table:    tview.NewTable(),
		app:      app,
		title:    title,
		headers:  headers,
		sortCol:  -1,
		sortAsc:  true,
		marked:   map[string]bool{},
		keys:     map[rune]func(){},
		ctrlKeys: map[tcell.Key]func(){},
	}
	t.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	t.SetSelectable(true, false)
	t.SetFixed(1, 0)
	t.SetSelectedFunc(func(row, _ int) {
		if r := t.rowAt(row); r != nil && t.onEnter != nil {
			t.onEnter(r)
		}
	})
	t.SetSelectionChangedFunc(func(row, _ int) {
		if r := t.rowAt(row); r != nil && t.onSelect != nil {
			t.onSelect(r)
		}
	})
	t.SetInputCapture(t.capture)
	t.render()
	return t
}

// Draw applies the current theme before drawing.
func (t *TableView) Draw(screen tcell.Screen) {
	t.SetBackgroundColor(theme.Bg)
	t.SetBorderColor(theme.Border)
	t.SetTitleColor(theme.Title)
	t.SetSelectedStyle(tcell.StyleDefault.Background(theme.SelectBg).Foreground(theme.SelectFg).Bold(true))
	t.Table.Draw(screen)
	_, _, w, _ := t.GetInnerRect()
	if wide := t.width > w; wide != t.wide {
		t.wide = wide
		go t.app.QueueUpdateDraw(t.app.updateHints)
	}
}

// Wide reports whether the columns do not fit the view.
func (t *TableView) Wide() bool { return t.wide }

// OnEnter sets the <enter> action.
func (t *TableView) OnEnter(f func(r *Row)) { t.onEnter = f }

// OnSelect is called when the cursor moves to another row.
func (t *TableView) OnSelect(f func(r *Row)) { t.onSelect = f }

// Bind adds a rune key action.
func (t *TableView) Bind(r rune, f func()) { t.keys[r] = f }

// BindKey adds a special key action.
func (t *TableView) BindKey(k tcell.Key, f func()) { t.ctrlKeys[k] = f }

// HandleKey runs the table's key handling on ev, for views that route keys
// to an embedded table; it returns ev when the table does not use it.
func (t *TableView) HandleKey(ev *tcell.EventKey) *tcell.EventKey { return t.capture(ev) }

func (t *TableView) capture(ev *tcell.EventKey) *tcell.EventKey {
	_, _, _, height := t.GetInnerRect()
	half := max(1, height/2)
	switch ev.Key() {
	case tcell.KeyCtrlD:
		t.move(half)
		return nil
	case tcell.KeyCtrlU:
		t.move(-half)
		return nil
	case tcell.KeyCtrlE:
		t.move(1)
		return nil
	case tcell.KeyCtrlY:
		t.move(-1)
		return nil
	case tcell.KeyRune:
		r := ev.Rune()
		if f, ok := t.keys[r]; ok {
			f()
			return nil
		}
		switch r {
		case ' ':
			t.toggleMark()
			return nil
		case 'o':
			t.cycleSort()
			return nil
		case 'O':
			t.sortAsc = !t.sortAsc
			t.render()
			t.flashSort()
			return nil
		case 'H':
			row, _ := t.GetOffset()
			t.Select(clamp(row+1, 1, t.GetRowCount()-1), 0)
			return nil
		case 'L':
			_, _, _, h := t.GetInnerRect()
			row, _ := t.GetOffset()
			t.Select(clamp(row+h-1, 1, t.GetRowCount()-1), 0)
			return nil
		case 'M':
			_, _, _, h := t.GetInnerRect()
			row, _ := t.GetOffset()
			t.Select(clamp(row+h/2, 1, t.GetRowCount()-1), 0)
			return nil
		}
	default:
		if f, ok := t.ctrlKeys[ev.Key()]; ok {
			f()
			return nil
		}
	}
	return ev
}

func (t *TableView) move(delta int) {
	row, _ := t.GetSelection()
	if n := t.GetRowCount(); n > 1 {
		t.Select(clamp(row+delta, 1, n-1), 0)
	}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

func (t *TableView) toggleMark() {
	r := t.Selected()
	if r == nil {
		return
	}
	if t.marked[r.Key] {
		delete(t.marked, r.Key)
	} else {
		t.marked[r.Key] = true
	}
	t.render()
	t.move(1)
}

func (t *TableView) cycleSort() {
	t.sortCol++
	if t.sortCol >= len(t.headers) {
		t.sortCol = -1
	}
	t.render()
	t.flashSort()
}

// flashSort says how the rows are sorted now.
func (t *TableView) flashSort() {
	if t.sortCol >= 0 {
		t.app.Flash().Infof("sort by %s %s", T(t.headers[t.sortCol]), ascLabel(t.sortAsc))
	} else {
		t.app.Flash().Info(T("sort: natural order"))
	}
}

func ascLabel(asc bool) string {
	if asc {
		return "↑"
	}
	return "↓"
}

// SetRows replaces content keeping the cursor on the same key.
func (t *TableView) SetRows(rows []Row) {
	cur := ""
	if r := t.Selected(); r != nil {
		cur = r.Key
	}
	t.rows = rows
	t.loading = false
	keys := map[string]bool{}
	for _, r := range rows {
		keys[r.Key] = true
	}
	for k := range t.marked {
		if !keys[k] {
			delete(t.marked, k)
		}
	}
	t.render()
	t.selectKey(cur)
}

// SetSort sorts by column col, ascending or not; -1 is the natural order.
func (t *TableView) SetSort(col int, asc bool) {
	t.sortCol, t.sortAsc = col, asc
	t.render()
}

// SetHeaders replaces the column headers (call SetRows after it).
func (t *TableView) SetHeaders(h []string) {
	t.headers = h
	if t.sortCol >= len(h) {
		t.sortCol = -1
	}
}

// Headers returns the column headers as given (untranslated).
func (t *TableView) Headers() []string { return t.headers }

// Rows returns all rows, filtered out ones included.
func (t *TableView) Rows() []Row { return t.rows }

// VisibleCount is the number of rows the filter lets through.
func (t *TableView) VisibleCount() int { return len(t.visible) }

// Rerender redraws the table, e.g. after a theme switch.
func (t *TableView) Rerender() { t.render() }

// SetLoading marks the table as loading (shown in the title).
func (t *TableView) SetLoading(b bool) {
	t.loading = b
	t.updateTitle()
}

// SetTitleInfo sets extra text shown in the title.
func (t *TableView) SetTitleInfo(s string) {
	t.extra = s
	t.updateTitle()
}

// SetFilter applies a live filter (regex if valid, else substring; "!" prefix inverts).
func (t *TableView) SetFilter(f string) {
	t.filter = f
	t.filterR = nil
	if pat := strings.TrimPrefix(f, "!"); pat != "" {
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(pat))
		}
		t.filterR = re
	}
	t.render()
	if t.GetRowCount() > 1 {
		t.Select(1, 0)
	}
}

// Filter returns the active filter.
func (t *TableView) Filter() string { return t.filter }

func (t *TableView) matches(r *Row) bool {
	if t.filterR == nil {
		return true
	}
	texts := make([]string, len(r.Cells))
	for i := range r.Cells {
		texts[i] = r.text(i)
	}
	hit := t.filterR.MatchString(strings.Join(texts, " "))
	return hit != strings.HasPrefix(t.filter, "!")
}

func (t *TableView) render() {
	t.Clear()
	for i, h := range t.headers {
		label := T(h)
		if i == t.sortCol {
			label += ascLabel(t.sortAsc)
		}
		t.SetCell(0, i, tview.NewTableCell(label).
			SetTextColor(theme.Header).SetAttributes(tcell.AttrBold).
			SetSelectable(false).SetExpansion(1))
	}
	t.visible = t.visible[:0]
	for i := range t.rows {
		if t.matches(&t.rows[i]) {
			t.visible = append(t.visible, i)
		}
	}
	if t.sortCol >= 0 {
		col, asc := t.sortCol, t.sortAsc
		sort.SliceStable(t.visible, func(a, b int) bool {
			return sortLess(&t.rows[t.visible[a]], &t.rows[t.visible[b]], col, asc)
		})
	}
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = StrWidth(T(h)) + 1 // the sort arrow
	}
	for vi, ri := range t.visible {
		r := &t.rows[ri]
		mark := t.marked[r.Key]
		for ci := range t.headers {
			widths[ci] = max(widths[ci], min(StrWidth(r.text(ci)), maxCellWidth))
			text := cell(r.Cells, ci)
			if !r.Markup[ci] {
				text = tview.Escape(text)
			}
			c := tview.NewTableCell(text).SetTextColor(r.color(ci)).SetExpansion(1).SetMaxWidth(maxCellWidth)
			if mark {
				c.SetTextColor(theme.Marked).SetAttributes(tcell.AttrBold)
			}
			if ci == 0 {
				c.SetReference(ri)
			}
			t.SetCell(vi+1, ci, c)
		}
	}
	t.width = len(widths) - 1 // a space between columns
	for _, w := range widths {
		t.width += w
	}
	if len(t.visible) == 0 {
		msg := T("no data")
		if t.loading {
			msg = T("loading…")
		} else if t.filter != "" {
			msg = T("no rows match filter")
		}
		t.SetCell(1, 0, tview.NewTableCell(msg).SetTextColor(theme.Dim).SetSelectable(false))
	}
	t.updateTitle()
}

func (t *TableView) updateTitle() {
	count := fmt.Sprintf("%d", len(t.visible))
	if t.filter != "" {
		count = fmt.Sprintf("%d/%d", len(t.visible), len(t.rows))
	}
	// "[n[]" renders as a literal "[n]" in tview
	title := fmt.Sprintf(" [::b]%s[::-]%s[%s[][-]", tview.Escape(T(t.title)), theme.Tag(theme.InfoValue), count)
	if t.extra != "" {
		title += fmt.Sprintf(" %s%s[-]", theme.Tag(theme.InfoValue), tview.Escape(t.extra))
	}
	if n := len(t.marked); n > 0 {
		title += fmt.Sprintf(" %s✓%d[-]", theme.Tag(theme.Marked), n)
	}
	if t.filter != "" {
		title += fmt.Sprintf(" %s</%s>[-]", theme.Tag(theme.Warn), tview.Escape(t.filter))
	}
	if t.loading {
		title += fmt.Sprintf(" %s⟳[-]", theme.Tag(theme.Warn))
	}
	t.SetTitle(title + " ")
}

func cell(cells []string, i int) string {
	if i < len(cells) {
		return cells[i]
	}
	return ""
}

// sortKey is what a cell sorts by: a number (the Sort value or a plain
// number, "%" allowed), else the text; class 0 numbers, 1 text, 2 empty.
func sortKey(r *Row, col int) (class int, num float64, text string) {
	if v, ok := r.Sort[col]; ok {
		return 0, v, ""
	}
	text = strings.TrimSpace(r.text(col))
	if text == "" {
		return 2, 0, ""
	}
	if f, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64); err == nil {
		return 0, f, ""
	}
	return 1, 0, strings.ToLower(text)
}

// sortLess orders rows by a column: numbers, then text, then empty cells.
// The direction applies within numbers and within text; empty cells stay
// last both ways, so "most first" does not start with rows lacking data.
func sortLess(a, b *Row, col int, asc bool) bool {
	ca, na, ta := sortKey(a, col)
	cb, nb, tb := sortKey(b, col)
	if ca != cb {
		return ca < cb
	}
	c := 0
	switch ca {
	case 0:
		c = cmp.Compare(na, nb)
	case 1:
		c = strings.Compare(ta, tb)
	}
	if asc {
		return c < 0
	}
	return c > 0
}

func (t *TableView) rowAt(tableRow int) *Row {
	if tableRow < 1 || tableRow-1 >= len(t.visible) {
		return nil
	}
	return &t.rows[t.visible[tableRow-1]]
}

// SelectedIndex is the position of the cursor among the visible rows.
func (t *TableView) SelectedIndex() int {
	row, _ := t.GetSelection()
	return row - 1
}

// SelectIndex moves the cursor to the n-th visible row.
func (t *TableView) SelectIndex(n int) {
	if len(t.visible) == 0 {
		return
	}
	t.Select(clamp(n+1, 1, len(t.visible)), 0)
}

// SelectKey moves the cursor to the row with key, else to the first row.
func (t *TableView) SelectKey(key string) { t.selectKey(key) }

// Selected returns the row under the cursor.
func (t *TableView) Selected() *Row {
	row, _ := t.GetSelection()
	return t.rowAt(row)
}

// Marked returns marked rows, or the current row when nothing is marked.
func (t *TableView) Marked() []*Row {
	var out []*Row
	for _, vi := range t.visible {
		if t.marked[t.rows[vi].Key] {
			out = append(out, &t.rows[vi])
		}
	}
	if len(out) == 0 {
		if r := t.Selected(); r != nil {
			out = append(out, r)
		}
	}
	return out
}

// ClearMarks unmarks all rows.
func (t *TableView) ClearMarks() {
	t.marked = map[string]bool{}
	t.render()
}

func (t *TableView) selectKey(key string) {
	if key != "" {
		for vi, ri := range t.visible {
			if t.rows[ri].Key == key {
				t.Select(vi+1, 0)
				return
			}
		}
	}
	t.Select(1, 0)
	t.ScrollToBeginning()
}
