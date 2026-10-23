package homer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Envelope is the {count, data} wrapper most homer-app list endpoints use.
type Envelope struct {
	Count   int             `json:"count"`
	Total   int             `json:"total"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// ListRaw GETs a list endpoint and returns its items as generic maps.
// It accepts {data:[...]}, {data:{...}} and bare arrays.
func (c *Client) ListRaw(ctx context.Context, path string) ([]map[string]any, error) {
	data, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return decodeItems(data)
}

func decodeItems(data []byte) ([]map[string]any, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var items []map[string]any
		return items, json.Unmarshal(data, &items)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(env.Data))
	switch {
	case raw == "" || raw == "null" || raw == `""`:
		return nil, nil
	case strings.HasPrefix(raw, "["):
		var items []map[string]any
		return items, json.Unmarshal(env.Data, &items)
	case strings.HasPrefix(raw, "{"):
		var item map[string]any
		if err := json.Unmarshal(env.Data, &item); err != nil {
			return nil, err
		}
		return []map[string]any{item}, nil
	}
	return nil, fmt.Errorf("unexpected data payload: %s", truncate(raw, 80))
}

// Profile is GET /users/profile.
type Profile struct {
	GUID         string `json:"guid"`
	Username     string `json:"username"`
	DisplayName  string `json:"displayname"`
	Group        string `json:"group"`
	Admin        bool   `json:"admin"`
	ExternalAuth bool   `json:"external_auth"`
}

// Profile returns the current user.
func (c *Client) Profile(ctx context.Context) (*Profile, error) {
	var env struct {
		Data Profile `json:"data"`
	}
	if err := c.GetJSON(ctx, "/users/profile", &env); err != nil {
		return nil, err
	}
	return &env.Data, nil
}

// Node is a database node from /database/node/list.
type Node struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Nodes lists database nodes that can be used in param.location.node.
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.GetJSON(ctx, "/database/node/list", &env); err != nil {
		return nil, err
	}
	var list []Node
	if err := json.Unmarshal(env.Data, &list); err == nil {
		return list, nil
	}
	// some versions return a map keyed by node name
	var m map[string]Node
	if err := json.Unmarshal(env.Data, &m); err != nil {
		return nil, err
	}
	for _, n := range m {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Value < list[j].Value })
	return list, nil
}

// Field is one entry of a mapping's fields_mapping.
type Field struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	FormType    string   `json:"form_type"`
	FormDefault []string `json:"form_default"`
	Position    int      `json:"position"`
	Hide        bool     `json:"hide"`
	Skip        bool     `json:"skip"`
}

// Mapping is a row of mapping_schema (one searchable protocol profile).
type Mapping struct {
	GUID          string          `json:"guid"`
	HepID         int             `json:"hepid"`
	HepAlias      string          `json:"hep_alias"`
	Profile       string          `json:"profile"`
	Version       int             `json:"version"`
	Retention     int             `json:"retention"`
	PartitionStep int             `json:"partition_step"`
	FieldsRaw     json.RawMessage `json:"fields_mapping"`
	Fields        []Field         `json:"-"`
}

// Key is the search key used by the API, e.g. "1_call".
func (m Mapping) Key() string { return fmt.Sprintf("%d_%s", m.HepID, m.Profile) }

// Title is a human label, e.g. "SIP call".
func (m Mapping) Title() string {
	alias := m.HepAlias
	if alias == "" {
		alias = strconv.Itoa(m.HepID)
	}
	return alias + " " + m.Profile
}

// Mappings returns all protocol mappings with parsed fields.
func (c *Client) Mappings(ctx context.Context) ([]Mapping, error) {
	var env struct {
		Data []Mapping `json:"data"`
	}
	if err := c.GetJSON(ctx, "/mapping/protocol", &env); err != nil {
		return nil, err
	}
	for i := range env.Data {
		m := &env.Data[i]
		_ = json.Unmarshal(m.FieldsRaw, &m.Fields)
		sort.SliceStable(m.Fields, func(a, b int) bool { return m.Fields[a].Position < m.Fields[b].Position })
	}
	sort.Slice(env.Data, func(i, j int) bool {
		if env.Data[i].HepID != env.Data[j].HepID {
			return env.Data[i].HepID < env.Data[j].HepID
		}
		return env.Data[i].Profile < env.Data[j].Profile
	})
	return env.Data, nil
}

// Condition is a single search form element.
type Condition struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
	HepID int    `json:"hepid"`
}

// Query is a high level search request.
type Query struct {
	Key        string // e.g. 1_call
	HepID      int
	Conditions []Condition
	Smart      string // smartinput expression, e.g. data_header.from_user = '100'
	OrLogic    bool
	Limit      int
	Nodes      []string
	From, To   time.Time
}

type timeRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type location struct {
	Node []string `json:"node"`
}

type timezone struct {
	Value int    `json:"value"`
	Name  string `json:"name"`
}

type searchParam struct {
	Transaction map[string]bool `json:"transaction"`
	Limit       int             `json:"limit"`
	Search      map[string]any  `json:"search"`
	OrLogic     bool            `json:"orlogic"`
	Location    location        `json:"location"`
	Timezone    timezone        `json:"timezone"`
	WhiteList   []string        `json:"whitelist,omitempty"`
}

type searchRequest struct {
	Param     searchParam `json:"param"`
	Timestamp timeRange   `json:"timestamp"`
}

func tr(from, to time.Time) timeRange {
	return timeRange{From: from.UnixMilli(), To: to.UnixMilli()}
}

// Body builds the JSON body for POST /search/call/data.
func (q Query) Body() any {
	conds := make([]Condition, 0, len(q.Conditions)+2)
	conds = append(conds, q.Conditions...)
	if q.Smart != "" {
		conds = append(conds, Condition{Name: "smartinput", Value: q.Smart, Type: "string", HepID: q.HepID})
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	conds = append(conds, Condition{Name: "limit", Value: strconv.Itoa(limit), Type: "integer", HepID: q.HepID})
	return searchRequest{
		Param: searchParam{
			Transaction: map[string]bool{},
			Limit:       limit,
			Search:      map[string]any{q.Key: conds},
			OrLogic:     q.OrLogic,
			Location:    location{Node: q.Nodes},
			Timezone:    timezone{Name: "UTC"},
		},
		Timestamp: tr(q.From, q.To),
	}
}

// SearchResult is the answer of POST /search/call/data.
type SearchResult struct {
	Total int              `json:"total"`
	Data  []map[string]any `json:"data"`
	Keys  []string         `json:"keys"`
}

// Search runs a search.
func (c *Client) Search(ctx context.Context, q Query) (*SearchResult, error) {
	data, err := c.Do(ctx, http.MethodPost, "/search/call/data", q.Body())
	if err != nil {
		return nil, err
	}
	var res SearchResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	return &res, nil
}

// CallRef identifies one or more sessions (Call-IDs) for transaction/export calls.
type CallRef struct {
	Key     string   // search key, e.g. 1_call
	IDs     []int64  // row ids
	CallIDs []string // sids
	Nodes   []string
	From    time.Time
	To      time.Time
}

type callSearch struct {
	ID     int64    `json:"id"`
	CallID []string `json:"callid"`
	UUID   []int64  `json:"uuid"`
}

func (r CallRef) body() any {
	var id int64
	if len(r.IDs) > 0 {
		id = r.IDs[0]
	}
	uuid := r.IDs
	if uuid == nil {
		uuid = []int64{}
	}
	key := r.Key
	if key == "" {
		key = "1_call"
	}
	return searchRequest{
		Param: searchParam{
			Transaction: map[string]bool{
				"call":         strings.HasSuffix(key, "_call"),
				"registration": strings.HasSuffix(key, "_registration"),
				"rest":         !strings.HasSuffix(key, "_call") && !strings.HasSuffix(key, "_registration"),
			},
			Limit:    200,
			Search:   map[string]any{key: callSearch{ID: id, CallID: r.CallIDs, UUID: uuid}},
			Location: location{Node: r.Nodes},
			Timezone: timezone{Name: "UTC"},
		},
		Timestamp: tr(r.From, r.To),
	}
}

// CallElement is one arrow of the call flow (data.calldata).
type CallElement struct {
	ID          float64 `json:"id"`
	Sid         string  `json:"sid"`
	SrcID       string  `json:"srcId"`
	DstID       string  `json:"dstId"`
	SrcIP       string  `json:"srcIp"`
	DstIP       string  `json:"dstIp"`
	SrcPort     float64 `json:"srcPort"`
	DstPort     float64 `json:"dstPort"`
	AliasSrc    string  `json:"aliasSrc"`
	AliasDst    string  `json:"aliasDst"`
	Method      string  `json:"method"`
	MethodText  string  `json:"method_text"`
	CreateDate  int64   `json:"create_date"`
	MicroTs     int64   `json:"micro_ts"`
	Protocol    float64 `json:"protocol"`
	RuriUser    string  `json:"ruri_user"`
	Destination int     `json:"destination"`
}

// Transaction is the answer of POST /call/transaction.
type Transaction struct {
	Total int `json:"total"`
	Data  struct {
		Messages []map[string]any `json:"messages"`
		Calldata []CallElement    `json:"calldata"`
		Alias    map[string]any   `json:"alias"`
		Hosts    map[string]struct {
			Host     []string `json:"host"`
			Position int      `json:"position"`
		} `json:"hosts"`
	} `json:"data"`
	Keys []string `json:"keys"`
}

// Transaction loads all correlated messages of the sessions.
func (c *Client) Transaction(ctx context.Context, r CallRef) (*Transaction, error) {
	data, err := c.Do(ctx, http.MethodPost, "/call/transaction", r.body())
	if err != nil {
		return nil, err
	}
	var t Transaction
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("decode transaction: %w", err)
	}
	return &t, nil
}

// QoS holds RTCP (hep 5) and RTP agent (hep 35) reports.
type QoS struct {
	RTCP struct {
		Data []map[string]any `json:"data"`
	} `json:"rtcp"`
	RTP struct {
		Data []map[string]any `json:"data"`
	} `json:"rtp"`
}

// QoS loads RTCP/RTP reports of the sessions.
func (c *Client) QoS(ctx context.Context, r CallRef) (*QoS, error) {
	data, err := c.Do(ctx, http.MethodPost, "/call/report/qos", r.body())
	if err != nil {
		return nil, err
	}
	var q QoS
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("decode qos: %w", err)
	}
	return &q, nil
}

// Logs loads HEP log messages (hep 100) of the sessions.
func (c *Client) Logs(ctx context.Context, r CallRef) ([]map[string]any, error) {
	data, err := c.Do(ctx, http.MethodPost, "/call/report/log", r.body())
	if err != nil {
		return nil, err
	}
	return decodeItems(data)
}

// ExportPCAP downloads the sessions as a PCAP file.
func (c *Client) ExportPCAP(ctx context.Context, r CallRef) ([]byte, error) {
	return c.Do(ctx, http.MethodPost, "/export/call/messages/pcap", r.body())
}

// ExportText downloads the sessions as plain text.
func (c *Client) ExportText(ctx context.Context, r CallRef) ([]byte, error) {
	return c.Do(ctx, http.MethodPost, "/export/call/messages/text", r.body())
}

// ImportPCAP uploads a PCAP file. With now=true timestamps are rewritten to the current time.
func (c *Client) ImportPCAP(ctx context.Context, file string, now bool) (string, error) {
	path := "/import/data/pcap"
	if now {
		path += "/now"
	}
	data, err := c.UploadFile(ctx, path, "fileKey", file)
	if err != nil {
		return "", err
	}
	var env Envelope
	if json.Unmarshal(data, &env) == nil && env.Message != "" {
		return env.Message, nil
	}
	return strings.TrimSpace(string(data)), nil
}

// MutationResult is the {message, data} answer of create/update/delete calls.
type MutationResult struct {
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// Mutate sends POST/PUT/DELETE and decodes the result.
func (c *Client) Mutate(ctx context.Context, method, path string, body any) (*MutationResult, error) {
	data, err := c.Do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	var r MutationResult
	if len(data) > 0 {
		_ = json.Unmarshal(data, &r)
	}
	return &r, nil
}
