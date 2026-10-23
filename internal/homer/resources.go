package homer

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/napadailo/voip-tui/i18n"
)

// Column describes a table column of a resource list.
type Column struct {
	Title string
	Field string // dotted path in the item, e.g. user_object.username
}

// Resource is a CRUD-able homer-app settings entity.
type Resource struct {
	Name     string   // command name
	Aliases  []string // command aliases
	Title    string
	Help     string
	ListPath string
	KeyField string
	// ItemPath builds the path for GET/PUT/DELETE of one item. Nil means ListPath + "/" + key.
	ItemPath func(key string) string
	// FetchItem means describe/edit should GET the item instead of using the list row.
	FetchItem bool
	// CreatePath overrides the POST path; it can depend on the new item.
	CreatePath func(item map[string]any) string
	Columns    []Column
	Create     bool
	Update     bool
	Delete     bool
	Admin      bool
	Template   func() string
	// Strip removes fields the API does not accept back on update.
	Strip []string
	// Derive makes columns named _something from an item and the whole
	// list, for what the item does not say outright.
	Derive func(item map[string]any, all []map[string]any) map[string]any
}

// PathFor returns the item path, the key escaped: a key from the server
// with "../" or "?" in it stays one segment.
func (r *Resource) PathFor(key string) string {
	if r.ItemPath != nil {
		return r.ItemPath(key)
	}
	return r.ListPath + "/" + url.PathEscape(key)
}

// Resources lists every settings entity exposed by homer-app.
var Resources = []*Resource{
	{
		Name: "aliases", Aliases: []string{"alias", "al"}, Title: i18n.N("Aliases"),
		Help:     i18n.N("IP/port → name aliases shown in call flows"),
		ListPath: "/alias", KeyField: "guid",
		Columns: []Column{{i18n.N("ALIAS"), "alias"}, {i18n.N("IP"), "ip"}, {i18n.N("PORT"), "port"}, {i18n.N("MASK"), "mask"},
			{i18n.N("CAPTURE ID"), "captureID"}, {i18n.N("ACTIVE"), "status"}, {i18n.N("GUID"), "guid"}},
		Create: true, Update: true, Delete: true, Admin: true,
		Strip: []string{"id"},
		Template: func() string {
			return `{
  "alias": "pbx-1",
  "ip": "10.0.0.10",
  "port": 5060,
  "mask": 32,
  "captureID": "0",
  "status": true
}`
		},
	},
	{
		Name: "users", Aliases: []string{"user", "us"}, Title: i18n.N("Users"),
		Help:     i18n.N("Homer users (password is required on create, optional on update)"),
		ListPath: "/users", KeyField: "guid",
		Columns: []Column{{i18n.N("USERNAME"), "username"}, {i18n.N("FIRST NAME"), "firstname"}, {i18n.N("LAST NAME"), "lastname"},
			{i18n.N("EMAIL"), "email"}, {i18n.N("GROUP"), "usergroup"}, {i18n.N("DEPARTMENT"), "department"}, {i18n.N("GUID"), "guid"}},
		Create: true, Update: true, Delete: true,
		Template: func() string {
			return `{
  "guid": "` + NewUUID() + `",
  "username": "jdoe",
  "password": "change-me",
  "firstname": "John",
  "lastname": "Doe",
  "email": "jdoe@example.com",
  "department": "VoIP",
  "usergroup": "user",
  "partid": 10
}`
		},
	},
	{
		Name: "tokens", Aliases: []string{"token", "authtokens", "tok"}, Title: i18n.N("API tokens"),
		Help:     i18n.N("Auth-Token keys for API access (the secret is shown only once, on create)"),
		ListPath: "/token/auth", KeyField: "guid",
		Columns: []Column{{i18n.N("NAME"), "name"}, {i18n.N("ACTIVE"), "active"}, {i18n.N("EXPIRES"), "expire_date"},
			{i18n.N("CALLS"), "usage_calls"}, {i18n.N("LIMIT"), "limit_calls"}, {i18n.N("LAST USED"), "lastusage_date"}, {i18n.N("GUID"), "guid"}},
		Create: true, Update: true, Delete: true, Admin: true,
		Template: func() string {
			exp := time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339)
			return fmt.Sprintf(`{
  "name": "homer-tui",
  "active": true,
  "expire_date": %q,
  "limit_calls": 100000
}`, exp)
		},
	},
	{
		Name: "mappings", Aliases: []string{"mapping", "map", "protocols", "proto"}, Title: i18n.N("Protocol mappings"),
		Help:     i18n.N("mapping_schema: searchable profiles, fields and correlation rules"),
		ListPath: "/mapping/protocol", KeyField: "guid",
		Columns: []Column{{i18n.N("KEY"), "_key"}, {i18n.N("WHAT"), "_what"}, {i18n.N("FIELDS"), "_fields"},
			{i18n.N("FLOW"), "_joins"}, {i18n.N("SEARCHED"), "_search"}, {i18n.N("ISSUES"), "_issues"}, {i18n.N("GUID"), "guid"}},
		Derive: DeriveMapping,
		Create: true, Update: true, Delete: true, Admin: true,
		Template: func() string {
			return `{
  "hepid": 1,
  "hep_alias": "SIP",
  "profile": "custom",
  "partid": 10,
  "version": 1,
  "retention": 14,
  "partition_step": 10,
  "fields_mapping": [],
  "correlation_mapping": []
}`
		},
	},
	{
		Name: "hepsub", Aliases: []string{"hepsubs", "hs"}, Title: i18n.N("HEP subscribe"),
		Help:     i18n.N("hepsub_mapping_schema: external agents lookup per protocol"),
		ListPath: "/hepsub/protocol", KeyField: "guid",
		Columns: []Column{{i18n.N("HEPID"), "hepid"}, {i18n.N("ALIAS"), "hep_alias"}, {i18n.N("PROFILE"), "profile"},
			{i18n.N("VERSION"), "version"}, {i18n.N("GUID"), "guid"}},
		Create: true, Update: true, Delete: true, Admin: true,
		Template: func() string {
			return `{
  "guid": "` + NewUUID() + `",
  "hepid": 1,
  "hep_alias": "SIP",
  "profile": "call",
  "version": 1,
  "mapping": {"lookup_id": "", "lookup_type": "", "lookup_field": "", "lookup_range": [-300, 200], "source_field": "data_header.callid"}
}`
		},
	},
	{
		Name: "advanced", Aliases: []string{"adv", "global", "settings"}, Title: i18n.N("Advanced (global settings)"),
		Help:     i18n.N("global_settings: grafana, prometheus, loki, export, transaction, scripts…"),
		ListPath: "/advanced", KeyField: "guid",
		Columns: []Column{{i18n.N("CATEGORY"), "category"}, {i18n.N("PARAM"), "param"}, {i18n.N("PARTID"), "partid"}, {i18n.N("GUID"), "guid"}},
		Create:  true, Update: true, Delete: true, Admin: true,
		Template: func() string {
			return `{
  "guid": "` + NewUUID() + `",
  "partid": 10,
  "category": "system",
  "param": "example",
  "data": {}
}`
		},
	},
	{
		Name: "usersettings", Aliases: []string{"prefs", "us-settings"}, Title: i18n.N("User settings"),
		Help:     i18n.N("Per-user preferences (user_settings)"),
		ListPath: "/user/settings", KeyField: "guid",
		Columns: []Column{{i18n.N("USERNAME"), "username"}, {i18n.N("CATEGORY"), "category"}, {i18n.N("PARAM"), "param"}, {i18n.N("PARTID"), "partid"}, {i18n.N("GUID"), "guid"}},
		Create:  true, Update: true, Delete: true,
		Template: func() string {
			return `{
  "guid": "` + NewUUID() + `",
  "category": "search",
  "param": "example",
  "partid": 10,
  "data": {}
}`
		},
	},
	{
		Name: "agents", Aliases: []string{"agent", "ag"}, Title: i18n.N("Agents"),
		Help:     i18n.N("Subscribed capture agents (hepsub)"),
		ListPath: "/agent/subscribe", KeyField: "uuid",
		Columns: []Column{{i18n.N("HOST"), "host"}, {i18n.N("PORT"), "port"}, {i18n.N("PROTOCOL"), "protocol"}, {i18n.N("NODE"), "node"},
			{i18n.N("TYPE"), "type"}, {i18n.N("GID"), "gid"}, {i18n.N("ACTIVE"), "active"}, {i18n.N("EXPIRES"), "expire_date"}, {i18n.N("UUID"), "uuid"}},
		Update: true, Delete: true,
	},
	{
		Name: "dashboards", Aliases: []string{"dashboard", "dash", "db"}, Title: i18n.N("Dashboards"),
		Help:     i18n.N("Web UI dashboards of the current user"),
		ListPath: "/dashboard/info", KeyField: "id",
		ItemPath: func(key string) string { return "/dashboard/store/" + url.PathEscape(key) },
		CreatePath: func(item map[string]any) string {
			id, _ := item["id"].(string)
			return "/dashboard/store/" + url.PathEscape(id)
		},
		FetchItem: true,
		Columns: []Column{{i18n.N("ID"), "id"}, {i18n.N("NAME"), "name"}, {i18n.N("PARAM"), "param"}, {i18n.N("TYPE"), "type"},
			{i18n.N("SHARED"), "shared"}, {i18n.N("OWNER"), "owner"}, {i18n.N("WEIGHT"), "weight"}},
		Create: true, Update: true, Delete: true,
		Template: func() string {
			return `{
  "id": "homer-tui-dashboard",
  "name": "homer-tui",
  "alias": "homer-tui",
  "param": "homer-tui",
  "shared": false,
  "type": 1,
  "weight": 50,
  "config": {"columns": 6, "gridType": "fixed", "ignoreMinSize": "warning", "maxrows": 4, "pushing": true},
  "widgets": []
}`
		},
	},
	{
		Name: "nodes", Aliases: []string{"node", "dbnodes"}, Title: i18n.N("Database nodes"),
		Help:     i18n.N("Database nodes used in param.location.node (read-only)"),
		ListPath: "/database/node/list", KeyField: "value",
		Columns: []Column{{i18n.N("NAME"), "name"}, {i18n.N("VALUE"), "value"}},
	},
}

// FindResource returns the resource by command name or alias.
func FindResource(name string) (*Resource, bool) {
	name = strings.ToLower(name)
	for _, r := range Resources {
		if r.Name == name {
			return r, true
		}
		for _, a := range r.Aliases {
			if a == name {
				return r, true
			}
		}
	}
	return nil, false
}

// Lookup reads a dotted path from a decoded JSON object.
func Lookup(item map[string]any, path string) any {
	var cur any = item
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

// Stringify renders a JSON value for a table cell.
func Stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, Stringify(e))
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "{…}"
	default:
		return fmt.Sprint(x)
	}
}

// NewUUID returns a random RFC 4122 v4 UUID.
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
