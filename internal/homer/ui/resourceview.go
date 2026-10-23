package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// ResourceView is a generic CRUD list for homer-app settings.
type ResourceView struct {
	*TableView
	app   *App
	res   *homer.Resource
	items []map[string]any // the list as loaded, for cards that compare
}

func newResourceView(app *App, res *homer.Resource) *ResourceView {
	headers := make([]string, len(res.Columns))
	for i, c := range res.Columns {
		headers[i] = c.Title
	}
	v := &ResourceView{TableView: NewTableView(app, res.Title, headers), app: app, res: res}
	v.OnEnter(func(*Row) { v.describe() })
	v.Bind('d', v.describe)
	v.Bind('y', v.json)
	v.Bind('r', v.Refresh)
	if res.Update {
		v.Bind('e', v.edit)
	}
	if res.Create {
		v.Bind('c', v.create)
		v.Bind('n', v.create)
	}
	if res.Delete {
		v.Bind('D', v.delete)
	}
	if res.Name == "mappings" {
		v.Bind('x', v.resetMapping)
		v.Bind('b', func() {
			if item, _, ok := v.selected(); ok {
				v.app.Push(newCorrBuilder(v, item))
			}
		})
	}
	if res.Name == "aliases" {
		v.Bind('T', v.toggleAlias)
	}
	v.Refresh()
	return v
}

func (v *ResourceView) Name() string      { return v.res.Name }
func (v *ResourceView) Table() *TableView { return v.TableView }
func (v *ResourceView) Close()            {}

func (v *ResourceView) Hints() []Hint {
	h := []Hint{{Key: "d", Desc: N("describe")}, {Key: "y", Desc: "JSON"}, {Key: "r", Desc: N("refresh")}}
	h = append([]Hint{{Key: "enter", Desc: N("describe")}}, h...)
	if v.res.Name == "mappings" {
		h = append(h, Hint{Key: "b", Desc: N("call assembly builder")}, Hint{Key: "x", Desc: N("reset to default")})
	}
	if v.res.Update {
		h = append(h, Hint{Key: "e", Desc: N("edit")})
	}
	if v.res.Create {
		h = append(h, Hint{Key: "c", Desc: N("create")})
	}
	if v.res.Delete {
		h = append(h, Hint{Key: "shift-d", Desc: N("delete")})
	}
	if v.res.Name == "aliases" {
		h = append(h, Hint{Key: "shift-t", Desc: N("toggle active")})
	}
	if v.res.Admin && !v.app.IsAdmin() {
		h = append(h, Hint{Key: "!", Desc: N("changes need admin")})
	}
	return h
}

// Refresh reloads the list.
func (v *ResourceView) Refresh() {
	v.SetLoading(true)
	res := v.res
	v.app.Async(func(ctx context.Context) func() {
		items, err := v.app.client.ListRaw(ctx, res.ListPath)
		return func() {
			v.SetLoading(false)
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			v.items = items
			rows := make([]Row, 0, len(items))
			for _, it := range items {
				var derived map[string]any
				if res.Derive != nil {
					derived = res.Derive(it, items)
				}
				cells := make([]string, len(res.Columns))
				for i, c := range res.Columns {
					if strings.HasPrefix(c.Field, "_") {
						cells[i] = fmtCell(derived[c.Field], v.app)
						continue
					}
					cells[i] = fmtCell(homer.Lookup(it, c.Field), v.app)
				}
				color := theme.Fg
				switch {
				case derived["_issues"] != nil && derived["_issues"] != "":
					color = theme.Warn
				case derived["_search"] == T("no"):
					color = theme.Dim
				}
				for _, f := range []string{"status", "active", "enabled"} {
					if b, ok := it[f].(bool); ok && !b {
						color = theme.Dim
					}
					if n, ok := it[f].(float64); ok && n == 0 {
						color = theme.Dim
					}
				}
				rows = append(rows, Row{Cells: cells, Data: it, Color: color, Key: homer.Stringify(it[res.KeyField])})
			}
			v.SetRows(rows)
		}
	})
}

func fmtCell(val any, app *App) string {
	if s, ok := val.(string); ok && len(s) >= 19 && s[4] == '-' && s[10] == 'T' {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			if t.Year() <= 1 {
				return ""
			}
			return t.In(app.loc).Format("2006-01-02 15:04")
		}
	}
	return homer.Stringify(val)
}

func (v *ResourceView) selected() (map[string]any, string, bool) {
	r := v.Selected()
	if r == nil {
		return nil, "", false
	}
	item, _ := r.Data.(map[string]any)
	return item, r.Key, item != nil
}

// fetch returns the full item (some lists only return summaries).
func (v *ResourceView) fetch(ctx context.Context, item map[string]any, key string) (map[string]any, error) {
	if !v.res.FetchItem {
		return item, nil
	}
	data, err := v.app.client.Do(ctx, http.MethodGet, v.res.PathFor(key), nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &env) == nil && len(env.Data) > 0 && env.Data[0] == '{' {
		var full map[string]any
		if json.Unmarshal(env.Data, &full) == nil {
			return full, nil
		}
	}
	var full map[string]any
	if err := json.Unmarshal(data, &full); err != nil {
		return nil, err
	}
	return full, nil
}

// describe opens the card of the item, read whole when the list has only
// part of it.
func (v *ResourceView) describe() {
	item, key, ok := v.selected()
	if !ok {
		return
	}
	v.app.Async(func(ctx context.Context) func() {
		full, err := v.fetch(ctx, item, key)
		return func() {
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			res := v.res
			build := func() *tui.Card { return resourceCard(res, full, key) }
			if res.Name == "mappings" {
				build = func() *tui.Card { return mappingCard(full, v.items) }
			}
			v.app.Push(newCardView(v.app, res.Name+"/"+key, build, func() any { return full }))
		}
	})
}

// json opens the item as Homer returned it; y again closes it.
func (v *ResourceView) json() {
	item, key, ok := v.selected()
	if !ok {
		return
	}
	v.app.Async(func(ctx context.Context) func() {
		full, err := v.fetch(ctx, item, key)
		return func() {
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			pushJSON(v.app, v.res.Name+"/"+key+" json", full)
		}
	})
}

func prettyJSONValue(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return buf.String()
}

func (v *ResourceView) edit() {
	item, key, ok := v.selected()
	if !ok {
		return
	}
	v.app.Async(func(ctx context.Context) func() {
		full, err := v.fetch(ctx, item, key)
		return func() {
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			obj := map[string]any{}
			for k, val := range full {
				obj[k] = val
			}
			for _, s := range v.res.Strip {
				delete(obj, s)
			}
			v.editLoop(prettyJSONValue(obj), func(edited map[string]any) {
				v.mutate(http.MethodPut, v.res.PathFor(key), edited, Tf("updated %s", key))
			})
		}
	})
}

func (v *ResourceView) create() {
	if v.res.Template == nil {
		return
	}
	// a template saved as it is may be meant: edit1 asks
	v.edit1(v.res.Template(), func(obj map[string]any) {
		path := v.res.ListPath
		if v.res.CreatePath != nil {
			path = v.res.CreatePath(obj)
		}
		v.mutate(http.MethodPost, path, obj, T("created"))
	}, true)
}

func (v *ResourceView) delete() {
	_, key, ok := v.selected()
	if !ok {
		return
	}
	r := v.Selected()
	label := key
	if len(r.Cells) > 0 && r.Cells[0] != "" {
		label = r.Cells[0] + " (" + key + ")"
	}
	v.app.Confirm(Tf("Delete %s %s?", strings.TrimSuffix(v.res.Name, "s"), label), T("Delete"), func() {
		v.mutate(http.MethodDelete, v.res.PathFor(key), nil, Tf("deleted %s", key))
	})
}

func (v *ResourceView) toggleAlias() {
	item, key, ok := v.selected()
	if !ok {
		return
	}
	obj := map[string]any{}
	for k, val := range item {
		obj[k] = val
	}
	active, _ := obj["status"].(bool)
	obj["status"] = !active
	v.mutate(http.MethodPut, v.res.PathFor(key), obj, fmt.Sprintf("alias %s active=%v", homer.Stringify(item["alias"]), !active))
}

func (v *ResourceView) mutate(method, path string, body any, okMsg string, done ...func()) {
	v.app.Async(func(ctx context.Context) func() {
		res, err := v.app.client.Mutate(ctx, method, path, body)
		return func() {
			var ae *homer.APIError
			if errors.As(err, &ae) && ae.Status == http.StatusForbidden && v.app.client.Username == "" {
				// homer-app issues API tokens with the "user" group
				v.app.Flash().Errf("%v: API tokens have user rights, use a host with an admin username/password for changes", err)
				return
			}
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			msg := okMsg
			if res.Message != "" {
				msg = res.Message
			}
			v.app.Flash().Info(msg)
			for _, f := range done {
				f()
			}
			// a new API token is returned only once
			if v.res.Name == "tokens" && method == http.MethodPost {
				var d struct {
					Token string `json:"token"`
				}
				if json.Unmarshal(res.Data, &d) == nil && d.Token != "" {
					v.app.Modal(Tf("New API token (shown only once):\n\n%s\n\nUse it as the 'token' of a host.", d.Token), []string{"OK"}, nil)
				}
			}
			v.Refresh()
		}
	})
}

// editLoop opens $EDITOR until the JSON is valid or the user gives up.
func (v *ResourceView) editLoop(initial string, apply func(map[string]any)) {
	v.edit1(initial, apply, false)
}

// edit1 opens the text in the editor and applies what comes back; bad JSON
// goes back to the editor. Text that comes back as it went is no change,
// unless it is a template, when it asks; an editor that came back at once
// most likely did not wait for the file to be saved, and says so.
func (v *ResourceView) edit1(initial string, apply func(map[string]any), template bool) {
	started := time.Now()
	edited, err := editInEditor(v.app, initial)
	if err != nil {
		v.app.Flash().Err(err)
		return
	}
	parse := func(text string) {
		var obj map[string]any
		if err := json.Unmarshal([]byte(stripComments(text)), &obj); err != nil {
			v.app.Modal(Tf("Invalid JSON: %v", err), []string{T("Edit again"), T("Discard")}, func(label string) {
				if label == T("Edit again") {
					v.edit1(text, apply, false)
				}
			})
			return
		}
		apply(obj)
	}
	if strings.TrimSpace(edited) == strings.TrimSpace(initial) {
		switch {
		case time.Since(started) < time.Second:
			v.app.Flash().Warnf("the editor %s came back at once, nothing saved; set HOMER_TUI_EDITOR to one that waits, such as code --wait", editorName())
		case template:
			v.app.Modal(T("The template is saved as it is. Create it so?"), []string{T("Create"), T("Edit again"), T("Discard")}, func(label string) {
				switch label {
				case T("Create"):
					parse(edited)
				case T("Edit again"):
					v.edit1(edited, apply, true)
				}
			})
		default:
			v.app.Flash().Info(T("no changes"))
		}
		return
	}
	parse(edited)
}

// editorName is the editor homer-tui runs: HOMER_TUI_EDITOR, VISUAL,
// EDITOR, vi.
func editorName() string {
	for _, e := range []string{"HOMER_TUI_EDITOR", "VISUAL", "EDITOR"} {
		if s := os.Getenv(e); s != "" {
			return s
		}
	}
	return "vi"
}

// stripComments drops lines starting with // so templates can carry hints.
func stripComments(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func editInEditor(app *App, content string) (string, error) {
	f, err := os.CreateTemp("", "homer-tui-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		return "", err
	}
	f.Close()
	editor := editorName()
	var runErr error
	app.Suspend(func() {
		cmd := exec.Command("sh", "-c", editor+` "$1"`, "homer-tui-editor", f.Name())
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		runErr = cmd.Run()
	})
	if runErr != nil {
		return "", fmt.Errorf(T("editor %q: %w"), editor, runErr)
	}
	data, err := os.ReadFile(f.Name())
	return string(data), err
}

func (v *ResourceView) resetMapping() {
	_, key, ok := v.selected()
	if !ok {
		return
	}
	v.app.Confirm(Tf("Reset mapping %s to the built-in default?", key), T("Reset"), func() {
		v.mutate(http.MethodGet, "/mapping/protocol/reset/"+key, nil, T("mapping reset"))
	})
}
