package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui/theme"
)

// SearchForm edits a SearchSpec: the profile and the time window, then the
// fields of the profile's set; ctrl-a adds one of the others, ctrl-d takes
// one out. A field with a value is always in the form.
type SearchForm struct {
	*tview.Form
	app    *App
	spec   homer.SearchSpec
	submit func(homer.SearchSpec)
	keys   []string
	items  []formItem // after the profile and the window
}

// formItem is a field of the form after the profile and the window: a
// field of the mapping or one of the options.
type formItem struct {
	name  string // as the set keeps it: a field id, or limit, nodes, or, where
	label string
	field *homer.Field
}

// The options of a search, offered as fields.
const (
	optLimit = "limit"
	optNodes = "nodes"
	optOr    = "or"
	optWhere = "where"
)

// defaultFormFields are the fields a profile's form starts with.
var defaultFormFields = map[string][]string{
	"1_call":         {"from_user", "to_user", "callid", "ruri_user"},
	"1_registration": {"from_user", "callid", "srcIp", "user_agent"},
	"1_default":      {"method", "from_user", "callid"},
}

func newSearchForm(app *App, spec homer.SearchSpec, submit func(homer.SearchSpec)) *SearchForm {
	f := &SearchForm{Form: tview.NewForm(), app: app, spec: spec, submit: submit}
	f.SetBorder(true).SetTitle(" " + T("search: enter or ctrl-s runs · ctrl-c clears · ctrl-a adds a field · ctrl-d takes one out · esc cancels") + " ")
	f.SetItemPadding(0)
	f.SetFieldBackgroundColor(tcell.ColorDarkSlateGray).SetLabelColor(theme.InfoKey)
	f.SetButtonStyle(theme.ButtonStyle().Background(tcell.ColorDarkSlateGray))
	f.SetButtonActivatedStyle(theme.ButtonActivatedStyle())
	f.SetCancelFunc(func() { app.Pop() })
	f.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		item, _ := f.GetFocusedItemIndex()
		switch ev.Key() {
		case tcell.KeyCtrlS:
			f.run()
			return nil
		case tcell.KeyCtrlA:
			f.addField()
			return nil
		case tcell.KeyCtrlD:
			f.removeField(item)
			return nil
		case tcell.KeyEnter:
			if _, ok := f.GetFormItem(max(item, 0)).(*tview.InputField); ok && item >= 0 {
				f.run()
				return nil
			}
		}
		return ev
	})
	f.build()
	return f
}

func (f *SearchForm) Name() string      { return "search form" }
func (f *SearchForm) Table() *TableView { return nil }
func (f *SearchForm) Refresh()          {}
func (f *SearchForm) Close()            {}
func (f *SearchForm) Hints() []Hint {
	return []Hint{{Key: "tab", Desc: N("next field")}, {Key: "enter/ctrl-s", Desc: N("search")}, {Key: "ctrl-c", Desc: N("clear")}, {Key: "ctrl-a", Desc: N("add a field")},
		{Key: "ctrl-d", Desc: N("take the field out")}, {Key: "esc", Desc: N("cancel")},
		{Key: "value", Desc: N("a;b = any of, 10% = like, !=x = not")}}
}

// set is the names of the fields the form of the profile shows: the set
// changed in this run, else the config's, else the built-in one, else the
// fields of the mapping it does not hide, three of them.
func (f *SearchForm) set() []string {
	key := f.spec.Key
	if s, ok := f.app.formFields[key]; ok {
		return s
	}
	if s, ok := f.app.cfg.SearchFields[key]; ok {
		return s
	}
	if s, ok := defaultFormFields[key]; ok {
		return s
	}
	var s []string
	if m := f.app.Mapping(key); m != nil {
		for _, fl := range m.Fields {
			if !fl.Skip && !fl.Hide && len(s) < 3 {
				s = append(s, fl.ID)
			}
		}
	}
	return s
}

// candidates are all the fields the form of the profile may show, in the
// order of the mapping, the options last.
func (f *SearchForm) candidates() []formItem {
	var out []formItem
	if m := f.app.Mapping(f.spec.Key); m != nil {
		for i := range m.Fields {
			fl := m.Fields[i]
			if fl.Skip {
				continue
			}
			label := fl.Name
			if label == "" {
				label = fl.ID
			}
			out = append(out, formItem{name: fl.ID, label: fmt.Sprintf("%s (%s)", label, fl.ID), field: &fl})
		}
	}
	return append(out, formItem{name: optLimit, label: T("Limit")}, formItem{name: optNodes, label: T("Nodes")},
		formItem{name: optOr, label: T("OR logic")}, formItem{name: optWhere, label: T("Where (smart)")})
}

// same tells an item from a name of the set: a field by any name
// ResolveField takes, an option by its own.
func (f *SearchForm) same(it formItem, name string) bool {
	if it.field == nil {
		return it.name == name
	}
	fl, ok := homer.ResolveField(name, f.app.Mapping(f.spec.Key))
	return ok && fl.ID == it.field.ID
}

// filter is the value the spec has for a field.
func (f *SearchForm) filter(fl *homer.Field) string {
	m := f.app.Mapping(f.spec.Key)
	for _, flt := range f.spec.Filters {
		if rf, ok := homer.ResolveField(flt.Field, m); ok && rf.ID == fl.ID {
			return flt.Value
		}
	}
	return ""
}

// valued says whether an item has a value of its own: a filter, or an
// option off its default.
func (f *SearchForm) valued(it formItem) bool {
	switch it.name {
	case optLimit:
		return f.spec.Limit > 0 && f.spec.Limit != f.app.cfg.DefaultLimit
	case optNodes:
		return len(f.spec.Nodes) > 0
	case optOr:
		return f.spec.OrLogic
	case optWhere:
		return f.spec.Smart != ""
	}
	return f.filter(it.field) != ""
}

// shown are the items of the form: those of the set in its order, then
// the others with a value; and the names of the set the profile lacks.
func (f *SearchForm) shown() ([]formItem, []string) {
	all := f.candidates()
	var out []formItem
	var unknown []string
	for _, name := range f.set() {
		known := false
		for _, it := range all {
			if f.same(it, name) {
				known = true
				if !slices.ContainsFunc(out, func(o formItem) bool { return o.name == it.name }) {
					out = append(out, it)
				}
			}
		}
		if !known {
			unknown = append(unknown, name)
		}
	}
	for _, it := range all {
		if f.valued(it) && !slices.ContainsFunc(out, func(o formItem) bool { return o.name == it.name }) {
			out = append(out, it)
		}
	}
	return out, unknown
}

func (f *SearchForm) build() {
	f.Clear(true)
	f.keys = nil
	current := 0
	for i, m := range f.app.Mappings() {
		f.keys = append(f.keys, m.Key())
		if m.Key() == f.spec.Key {
			current = i
		}
	}
	if len(f.keys) == 0 {
		f.keys = []string{f.spec.Key}
	}
	labels := make([]string, len(f.keys))
	for i, k := range f.keys {
		if m := f.app.Mapping(k); m != nil {
			labels[i] = fmt.Sprintf("%-16s %s", k, m.Title())
		} else {
			labels[i] = k
		}
	}
	f.AddDropDown(T("Profile"), labels, current, func(_ string, idx int) {
		if idx >= 0 && idx < len(f.keys) && f.keys[idx] != f.spec.Key {
			f.collect()
			f.spec.Key = f.keys[idx]
			f.spec.Filters = nil
			f.build()
			f.focus(0)
		}
	})
	f.AddInputField(T("Window"), f.spec.Window(f.app.loc), 42, nil, nil)
	f.input(T("Window")).SetPlaceholder("1h · 15:00..15:30 · -3h..-1h")

	var unknown []string
	f.items, unknown = f.shown()
	_, own := f.app.cfg.SearchFields[f.spec.Key] // the built-in sets name what the mapping may lack
	if len(unknown) > 0 && own && f.app.Mapping(f.spec.Key) != nil {
		f.app.Flash().Warnf("unknown fields ignored: %s (see :mappings)", strings.Join(unknown, ", "))
	}
	for _, it := range f.items {
		switch it.name {
		case optLimit:
			f.AddInputField(it.label, strconv.Itoa(f.spec.Limit), 8, tview.InputFieldInteger, nil)
		case optNodes:
			f.AddInputField(it.label, strings.Join(f.spec.Nodes, ","), 40, nil, nil)
			f.input(it.label).SetPlaceholder(Tf("all: %s", strings.Join(f.app.NodeValues(), ",")))
		case optOr:
			f.AddCheckbox(it.label, f.spec.OrLogic, nil)
		case optWhere:
			f.AddInputField(it.label, f.spec.Smart, 60, nil, nil)
			f.input(it.label).SetPlaceholder("data_header.from_user = '100' AND data_header.method = 'INVITE'")
		default:
			f.AddInputField(it.label, f.filter(it.field), 40, nil, nil)
			if len(it.field.FormDefault) > 0 {
				f.input(it.label).SetPlaceholder(strings.Join(it.field.FormDefault, ";"))
			}
		}
	}
	f.AddButton(T("Search"), f.run)
	f.AddButton(T("Clear"), func() { f.CtrlC() })
	f.AddButton(T("Cancel"), func() { f.app.Pop() })
}

func (f *SearchForm) input(label string) *tview.InputField {
	if it, ok := f.GetFormItemByLabel(label).(*tview.InputField); ok {
		return it
	}
	return tview.NewInputField()
}

func (f *SearchForm) text(label string) string {
	return strings.TrimSpace(f.input(label).GetText())
}

// collect reads form values into f.spec; returns an error on invalid input.
func (f *SearchForm) collect() error {
	if err := homer.ParseWindow(f.text(T("Window")), &f.spec, f.app.loc, time.Now()); err != nil {
		return err
	}
	f.spec.Filters = nil
	for _, it := range f.items {
		switch it.name {
		case optLimit:
			if n, err := strconv.Atoi(f.text(it.label)); err == nil && n > 0 {
				f.spec.Limit = n
			}
		case optNodes:
			f.spec.Nodes = nil
			for _, n := range strings.Split(f.text(it.label), ",") {
				if n = strings.TrimSpace(n); n != "" {
					f.spec.Nodes = append(f.spec.Nodes, n)
				}
			}
		case optOr:
			if cb, ok := f.GetFormItemByLabel(it.label).(*tview.Checkbox); ok {
				f.spec.OrLogic = cb.IsChecked()
			}
		case optWhere:
			f.spec.Smart = f.text(it.label)
		default:
			if v := f.text(it.label); v != "" {
				f.spec.Filters = append(f.spec.Filters, homer.Filter{Field: it.field.ID, Value: v})
			}
		}
	}
	return nil
}

// CtrlC clears the values of the fields; the profile, the window and the
// fields of the form stay.
func (f *SearchForm) CtrlC() bool {
	item, button := f.GetFocusedItemIndex()
	if err := f.collect(); err != nil {
		f.app.Flash().Err(err)
		return true
	}
	f.spec.Filters, f.spec.Smart = nil, ""
	f.build() // a field shown for its value only goes
	if button >= 0 {
		f.focus(f.GetFormItemCount() + button)
	} else {
		f.focus(min(max(item, 0), f.GetFormItemCount()-1))
	}
	return true
}

func (f *SearchForm) run() {
	if err := f.collect(); err != nil {
		f.app.Flash().Err(err)
		return
	}
	f.submit(f.spec)
}

// names is the set of the form as it is now, by the names of its items.
func (f *SearchForm) names() []string {
	out := make([]string, 0, len(f.items))
	for _, it := range f.items {
		out = append(out, it.name)
	}
	return out
}

// addField offers the fields the form lacks; enter puts one in the set of
// the profile until homer-tui quits.
func (f *SearchForm) addField() {
	if err := f.collect(); err != nil {
		f.app.Flash().Err(err)
		return
	}
	t := NewTableView(f.app, N("Add a field to the search form"), []string{N("FIELD"), N("WHAT")})
	var rows []Row
	for _, it := range f.candidates() {
		if slices.Contains(f.names(), it.name) {
			continue
		}
		what := T("a field of the profile")
		switch it.name {
		case optLimit:
			what = T("how many messages a page has")
		case optNodes:
			what = T("the nodes searched, all when empty")
		case optOr:
			what = T("any field matches instead of all")
		case optWhere:
			what = T("an expression homer-app's smart input takes")
		default:
			if p := homer.FieldPlace(it.field.ID); p != "" {
				what = p
			}
		}
		rows = append(rows, Row{Cells: []string{it.label, what}, Data: it.name, Key: it.name})
	}
	if len(rows) == 0 {
		f.app.Flash().Info(T("every field is in the form"))
		return
	}
	t.SetRows(rows)
	t.OnEnter(func(r *Row) {
		f.app.Pop()
		name := r.Data.(string)
		f.app.formFields[f.spec.Key] = append(f.names(), name)
		f.build()
		for i, it := range f.items {
			if it.name == name {
				f.focus(2 + i)
			}
		}
	})
	f.app.Push(&fieldPicker{TableView: t})
}

// removeField takes the field at index i of the form out of the set of the
// profile, and its value with it.
func (f *SearchForm) removeField(i int) {
	if i < 2 || i-2 >= len(f.items) {
		f.app.Flash().Info(T("the profile and the window are always in the form"))
		return
	}
	if err := f.collect(); err != nil {
		f.app.Flash().Err(err)
		return
	}
	it := f.items[i-2]
	switch it.name {
	case optLimit:
		f.spec.Limit = f.app.cfg.DefaultLimit
	case optNodes:
		f.spec.Nodes = nil
	case optOr:
		f.spec.OrLogic = false
	case optWhere:
		f.spec.Smart = ""
	default:
		f.spec.Filters = slices.DeleteFunc(f.spec.Filters, func(flt homer.Filter) bool { return flt.Field == it.field.ID })
	}
	f.app.formFields[f.spec.Key] = slices.DeleteFunc(f.names(), func(n string) bool { return n == it.name })
	f.build()
	f.focus(min(i, f.GetFormItemCount()-1))
}

// focus moves to the item at index i of a form built anew: tview gives
// its items tab and backtab only when the form itself gets the focus.
func (f *SearchForm) focus(i int) {
	f.SetFocus(i)
	f.app.SetFocus(f)
}

// fieldPicker is the list addField offers.
type fieldPicker struct{ *TableView }

func (p *fieldPicker) Name() string      { return T("add a field") }
func (p *fieldPicker) Table() *TableView { return p.TableView }
func (p *fieldPicker) Refresh()          {}
func (p *fieldPicker) Close()            {}
func (p *fieldPicker) Hints() []Hint {
	return []Hint{{Key: "enter", Desc: N("add")}, {Key: "/", Desc: N("filter")}, {Key: "esc", Desc: N("cancel")}}
}
