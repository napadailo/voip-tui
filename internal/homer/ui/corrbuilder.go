package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// CorrBuilderView puts the call assembly of a mapping together from
// blocks: the list of blocks, and under it how a call will be assembled
// and what homer-app would trip on, as the blocks are now.
type CorrBuilderView struct {
	*tview.Flex
	app     *App
	rv      *ResourceView
	list    *TableView
	preview *tview.TextView
	item    map[string]any // the mapping as homer-app gave it
	hepid   int
	key     string
	blocks  []homer.Block
	saved   string // the rules homer-app has, as JSON
	opened  string // the rules of the blocks as they were read or last saved: a change is from these
}

func newCorrBuilder(rv *ResourceView, item map[string]any) *CorrBuilderView {
	v := &CorrBuilderView{app: rv.app, rv: rv, item: item}
	fmt.Sscan(homer.Stringify(item["hepid"]), &v.hepid)
	v.key = fmt.Sprintf("%d_%s", v.hepid, str(item, "profile"))
	rules, _ := homer.CorrRules(item["correlation_mapping"])
	v.blocks = homer.ParseBlocks(rules, v.hepid)
	v.saved = rulesJSON(rules)
	v.opened = rulesJSON(homer.CompileBlocks(v.blocks, v.hepid))
	v.list = NewTableView(rv.app, N("call assembly"), []string{N("ON"), N("BLOCK"), N("PROFILE"), N("MATCH"), N("LOOKUP"), N("WINDOW")})
	v.list.SetTitleInfo(v.key)
	v.list.OnSelect(func(*Row) { v.showPreview() })
	v.list.OnEnter(func(*Row) { v.editBlock() })
	v.list.Bind(' ', v.toggle)
	v.list.Bind('e', v.editBlock) // as e edits everywhere else
	v.list.Bind('a', v.addBlock)
	v.list.Bind('D', v.deleteBlock)
	v.list.Bind('J', func() { v.moveBlock(1) })
	v.list.Bind('K', func() { v.moveBlock(-1) })
	v.list.Bind('s', v.save)
	v.preview = tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true)
	v.preview.SetBorder(true).SetTitle(" "+T("how a call will be assembled")+" ").SetBorderPadding(0, 0, 1, 1)
	v.Flex = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(v.list, 0, 1, true).AddItem(v.preview, 0, 1, false)
	v.render(0)
	return v
}

func rulesJSON(rules []homer.CorrRule) string {
	b, _ := json.Marshal(rules)
	return string(b)
}

func (v *CorrBuilderView) Name() string      { return v.key + " " + T("call assembly") }
func (v *CorrBuilderView) Table() *TableView { return v.list }
func (v *CorrBuilderView) Refresh()          { v.render(v.list.SelectedIndex()) }
func (v *CorrBuilderView) Close()            {}

func (v *CorrBuilderView) Hints() []Hint {
	return []Hint{{Key: "space", Desc: N("on/off")}, {Key: "enter/e", Desc: N("set up")}, {Key: "a", Desc: N("add a block")},
		{Key: "shift-d", Desc: N("delete")}, {Key: "shift-j/k", Desc: N("move down/up")}, {Key: "s", Desc: N("save")}}
}

func (v *CorrBuilderView) rules() []homer.CorrRule { return homer.CompileBlocks(v.blocks, v.hepid) }

// fields are the mapping's own, the ones a rule's source_field can be.
func (v *CorrBuilderView) fields() []homer.MappingField {
	f, _ := homer.MappingFields(v.item["fields_mapping"])
	return f
}

// dirty tells blocks changed here. The rules of blocks only read may
// differ from homer-app's, repeats folded and the legs first: that is no
// change of the user's, and s writes them so only when asked.
func (v *CorrBuilderView) dirty() bool { return rulesJSON(v.rules()) != v.opened }

// rewrites tells blocks that write other rules than homer-app has.
func (v *CorrBuilderView) rewrites() bool { return rulesJSON(v.rules()) != v.saved }

// Back asks before esc leaves blocks not saved.
func (v *CorrBuilderView) Back() bool {
	if !v.dirty() {
		return false
	}
	v.app.Confirm(T("Leave without saving the call assembly?"), T("Leave"), v.app.Pop)
	return true
}

func (v *CorrBuilderView) render(sel int) {
	rows := make([]Row, 0, len(v.blocks))
	for i, b := range v.blocks {
		mark, color := "✓", theme.Fg
		if !b.On {
			mark, color = "·", theme.Dim
		}
		match, lookup := homer.BlockMatch(b)
		rows = append(rows, Row{Cells: []string{mark, homer.BlockTag(b, v.hepid), homer.BlockProfile(b), match, lookup, homer.BlockWindow(b)},
			Color: color, Data: i, Key: strconv.Itoa(i)})
	}
	v.list.SetRows(rows)
	if len(rows) > 0 {
		v.list.SelectIndex(max(0, min(sel, len(rows)-1)))
	}
	v.showPreview()
}

// showPreview says what the selected block does, how a call will be
// assembled from all of them, and what homer-app would trip on.
func (v *CorrBuilderView) showPreview() {
	var b strings.Builder
	if i := v.list.SelectedIndex(); i >= 0 && i < len(v.blocks) {
		bl := v.blocks[i]
		fmt.Fprintf(&b, "%s[::b]%s[::-][-]  %s\n\n", theme.Tag(theme.Title), tview.Escape(homer.BlockName(bl, v.hepid)), tui.Faint(tview.Escape(homer.BlockHelp(bl.Kind))))
	}
	rules := v.rules()
	b.WriteString(tui.Faint(Tf("as s writes it: %d rules", len(rules))) + "\n")
	for _, l := range flowLines(rules, nil, ruleIssues(homer.RuleIssues(rules, v.fields())), v.hepid, v.key) {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n")
	for _, ri := range homer.RuleIssues(rules, v.fields()) {
		b.WriteString(theme.Tag(theme.Warn) + "⚠ " + tview.Escape(ri.Text) + "[-]\n")
	}
	before, _ := homer.CorrRules(v.item["correlation_mapping"])
	state := T("saved")
	switch {
	case v.dirty():
		state = theme.Tag(theme.Warn) + Tf("not saved: %d rules → %d, s saves", len(before), len(rules)) + "[-]"
	case v.rewrites():
		state = Tf("saved; from the blocks the rules come out otherwise, %d → %d, s writes them so", len(before), len(rules))
	}
	b.WriteString("\n" + state)
	v.preview.SetText(b.String())
}

// rulesValue makes the rules the JSON value of correlation_mapping.
func rulesValue(rules []homer.CorrRule) any {
	var v any
	_ = json.Unmarshal([]byte(rulesJSON(rules)), &v)
	if v == nil {
		v = []any{}
	}
	return v
}

func (v *CorrBuilderView) selected() int {
	i := v.list.SelectedIndex()
	if i < 0 || i >= len(v.blocks) {
		return -1
	}
	return i
}

func (v *CorrBuilderView) toggle() {
	if i := v.selected(); i >= 0 {
		v.blocks[i].On = !v.blocks[i].On
		v.render(i)
	}
}

// deleteBlock asks first, as shift-d does everywhere: a custom rule may
// carry JS written by hand; space only turns a block off.
func (v *CorrBuilderView) deleteBlock() {
	i := v.selected()
	if i < 0 {
		return
	}
	v.app.Confirm(Tf("Delete the block %s? space only turns it off", homer.BlockName(v.blocks[i], v.hepid)), T("Delete"), func() {
		v.blocks = append(v.blocks[:i], v.blocks[i+1:]...)
		v.render(i)
	})
}

func (v *CorrBuilderView) moveBlock(d int) {
	i := v.selected()
	if i < 0 || i+d < 0 || i+d >= len(v.blocks) {
		return
	}
	if v.blocks[i].Kind == homer.BlockLegs || v.blocks[i+d].Kind == homer.BlockLegs {
		v.app.Flash().Info(T("the legs block goes first: the session ids it finds go to the blocks after it"))
		return
	}
	v.blocks[i], v.blocks[i+d] = v.blocks[i+d], v.blocks[i]
	v.render(i + d)
}

// addBlock offers the kinds of blocks one under another, and what the one
// selected brings into a call under the list; enter sets one up and adds
// it, esc goes back.
func (v *CorrBuilderView) addBlock() {
	t := NewTableView(v.app, N("Add a block to the call assembly"), []string{N("BLOCK"), N("MATCH"), N("LOOKUP")})
	var rows []Row
	for _, k := range homer.BlockKinds() {
		b := homer.NewBlock(k)
		tag := homer.BlockTag(b, v.hepid)
		match, lookup := homer.BlockMatch(b)
		if k == homer.BlockCustom {
			tag, match, lookup = T("custom"), "…", "…"
		}
		rows = append(rows, Row{Cells: []string{tag, match, lookup}, Data: k, Key: k})
	}
	help := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true)
	help.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	t.OnSelect(func(r *Row) {
		k := r.Data.(string)
		title := r.Cells[0]
		if k != homer.BlockCustom {
			title += " " + r.Cells[1] + " → " + r.Cells[2]
		}
		help.SetTitle(" " + title + " ")
		help.SetText(tview.Escape(homer.BlockHelp(k)))
	})
	t.SetRows(rows)
	t.OnEnter(func(r *Row) {
		// the setup opens over the list: esc there goes back to it and
		// adds nothing, done closes the list too
		v.setUp(homer.NewBlock(r.Data.(string)), true, func(b homer.Block) {
			v.app.Pop()
			at := len(v.blocks)
			if b.Kind == homer.BlockLegs { // first, as it is written
				at = 0
			}
			v.blocks = slices.Insert(v.blocks, at, b)
			v.render(at)
		})
	})
	v.app.Push(&blockPicker{Flex: tview.NewFlex().SetDirection(tview.FlexRow).AddItem(t, 0, 1, true).AddItem(help, 6, 0, false), list: t})
}

// blockPicker is the list addBlock offers, with the help of the block
// selected under it.
type blockPicker struct {
	*tview.Flex
	list *TableView
}

func (p *blockPicker) Name() string      { return T("add a block") }
func (p *blockPicker) Table() *TableView { return p.list }
func (p *blockPicker) Refresh()          {}
func (p *blockPicker) Close()            {}
func (p *blockPicker) Hints() []Hint {
	return []Hint{{Key: "enter", Desc: N("add and set up")}, {Key: "esc", Desc: N("cancel")}}
}

// editBlock sets the selected block up.
func (v *CorrBuilderView) editBlock() {
	i := v.selected()
	if i < 0 {
		return
	}
	v.setUp(v.blocks[i], false, func(b homer.Block) {
		v.blocks[i] = b
		v.render(i)
	})
}

// setUp sets a block up: its window, where it looks, its suffix; a custom
// rule opens in the editor as JSON. apply gets it when done; esc, Cancel or
// an editor that brings nothing back leave all as it was. A new custom
// rule saved as it came asks whether to add it so.
func (v *CorrBuilderView) setUp(b homer.Block, isNew bool, apply func(homer.Block)) {
	if b.Kind == homer.BlockCustom {
		data, _ := json.MarshalIndent(b.Rule, "", "  ")
		v.rv.edit1(string(data), func(obj map[string]any) {
			raw, _ := json.Marshal(obj)
			var r homer.CorrRule
			if err := json.Unmarshal(raw, &r); err != nil {
				v.app.Flash().Err(err)
				return
			}
			b.Rule = r
			apply(b)
		}, isNew)
		return
	}
	f := tview.NewForm()
	f.SetBorder(true).SetTitle(" " + homer.BlockName(b, v.hepid) + " · " + T("tab moves, enter on Done applies, esc cancels") + " ")
	f.SetItemPadding(0)
	f.SetFieldBackgroundColor(tcell.ColorDarkSlateGray).SetLabelColor(theme.InfoKey)
	f.SetButtonStyle(theme.ButtonStyle().Background(tcell.ColorDarkSlateGray))
	f.SetButtonActivatedStyle(theme.ButtonActivatedStyle())
	profiles := []string{"call", "registration", "default"}
	profile := 0
	for j, p := range profiles {
		if p == b.Profile {
			profile = j
		}
	}
	f.AddInputField(T("seconds before the search window"), strconv.Itoa(-b.From), 8, tview.InputFieldInteger, nil)
	f.AddInputField(T("seconds after it"), strconv.Itoa(b.To), 8, tview.InputFieldInteger, nil)
	switch b.Kind {
	case homer.BlockLegs, homer.BlockXCID, homer.BlockSuffix, homer.BlockSIP:
		f.AddDropDown(T("profile it looks in"), profiles, profile, nil)
	}
	if b.Kind == homer.BlockSuffix {
		f.AddInputField(T("suffix of the leg's Call-ID"), b.Suffix, 20, nil, nil)
	}
	if b.Kind == homer.BlockRTP {
		by := 0
		if b.ByIP {
			by = 1
		}
		f.AddDropDown(T("match by"), []string{T("Call-ID"), T("the IP of the messages")}, by, nil)
	}
	f.AddButton(T("Done"), func() {
		nb := b
		before, _ := strconv.Atoi(f.GetFormItemByLabel(T("seconds before the search window")).(*tview.InputField).GetText())
		after, _ := strconv.Atoi(f.GetFormItemByLabel(T("seconds after it")).(*tview.InputField).GetText())
		nb.From, nb.To = -before, after
		if d, ok := f.GetFormItemByLabel(T("profile it looks in")).(*tview.DropDown); ok {
			_, nb.Profile = d.GetCurrentOption()
		}
		if in, ok := f.GetFormItemByLabel(T("suffix of the leg's Call-ID")).(*tview.InputField); ok {
			if s := in.GetText(); !homer.SuffixOK(s) {
				v.app.Flash().Errf("the suffix must not be empty nor have quotes or a backslash")
				return
			} else {
				nb.Suffix = s
			}
		}
		if d, ok := f.GetFormItemByLabel(T("match by")).(*tview.DropDown); ok {
			n, _ := d.GetCurrentOption()
			nb.ByIP = n == 1
		}
		v.app.Pop()
		apply(nb)
	})
	f.AddButton(T("Cancel"), v.app.Pop)
	f.SetCancelFunc(v.app.Pop)
	v.app.Push(&blockForm{Form: f, name: homer.BlockTag(b, v.hepid)})
}

// save writes the call assembly to homer-app after a question that says
// what changes; homer-app's update needs the whole mapping.
func (v *CorrBuilderView) save() {
	if !v.rewrites() {
		v.app.Flash().Info(T("no changes"))
		return
	}
	rules := v.rules()
	before, _ := homer.CorrRules(v.item["correlation_mapping"])
	v.app.Confirm(Tf("Save the call assembly of %s: %d rules → %d?", v.key, len(before), len(rules)), T("Save"), func() {
		obj := map[string]any{}
		for k, x := range v.item {
			obj[k] = x
		}
		obj["correlation_mapping"] = rulesValue(rules)
		v.rv.mutate(http.MethodPut, v.rv.res.PathFor(str(obj, "guid")), obj, T("saved"), func() {
			v.item, v.saved, v.opened = obj, rulesJSON(rules), rulesJSON(rules)
			v.showPreview()
		})
	})
}

// blockForm is the form of one block as a view of its own.
type blockForm struct {
	*tview.Form
	name string
}

func (f *blockForm) Name() string      { return f.name }
func (f *blockForm) Table() *TableView { return nil }
func (f *blockForm) Refresh()          {}
func (f *blockForm) Close()            {}
func (f *blockForm) Hints() []Hint {
	return []Hint{{Key: "tab", Desc: N("next field")}, {Key: "esc", Desc: N("cancel")}}
}
