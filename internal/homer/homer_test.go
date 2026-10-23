package homer_test

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/internal/homer/mock"
)

func newClient(t *testing.T) (*homer.Client, *mock.Server) {
	t.Helper()
	srv := mock.New(1)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c := homer.New(homer.Options{URL: ts.URL, Username: "admin", Password: "sipcapture"})
	if _, err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestParseSearchArgs(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, loc)
	base := homer.SearchSpec{Key: "1_call", Since: time.Hour, Limit: 200}
	spec, err := homer.ParseSearchArgs(`from_user=100 method=INVITE;BYE ua!=scanner since=2h limit=50 node=a,b or where data_header.to_user = '200'`, base, loc, now)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Since != 2*time.Hour || spec.Limit != 50 || !spec.OrLogic || len(spec.Nodes) != 2 {
		t.Fatalf("unexpected spec: %+v", spec)
	}
	if spec.Smart != "data_header.to_user = '200'" {
		t.Fatalf("smart = %q", spec.Smart)
	}
	if len(spec.Filters) != 3 || spec.Filters[2].Value != "!=scanner" || spec.Filters[1].Value != "INVITE;BYE" {
		t.Fatalf("filters = %+v", spec.Filters)
	}
	again, err := homer.ParseSearchArgs(spec.String(), homer.SearchSpec{Key: "1_call"}, loc, now)
	if err != nil {
		t.Fatal(err)
	}
	if again.String() != spec.String() {
		t.Fatalf("round trip:\n%s\n%s", spec.String(), again.String())
	}

	abs, err := homer.ParseSearchArgs(`from="2026-10-06 10:00" to=11:30`, base, loc, now)
	if err != nil {
		t.Fatal(err)
	}
	from, to := abs.Range(now)
	if from.Hour() != 10 || to.Hour() != 11 || to.Minute() != 30 {
		t.Fatalf("range %v %v", from, to)
	}
	if _, err := homer.ParseSearchArgs(`from=12:00 to=11:00`, base, loc, now); err == nil {
		t.Fatal("expected empty range error")
	}
	if _, err := homer.ParseSearchArgs(`bogus`, base, loc, now); err == nil {
		t.Fatal("expected parse error")
	}
	short, _ := homer.ParseSearchArgs(`15m`, base, loc, now)
	if short.Since != 15*time.Minute {
		t.Fatalf("bare duration: %v", short.Since)
	}
}

func TestDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"15m": 15 * time.Minute, "1d": 24 * time.Hour, "1h30m": 90 * time.Minute, "1w": 7 * 24 * time.Hour} {
		got, err := homer.ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
		if homer.FormatDuration(want) != in && in != "1w" {
			t.Errorf("format %v = %s", want, homer.FormatDuration(want))
		}
	}
	if _, err := homer.ParseDuration("5y"); err == nil {
		t.Error("expected error for unit y")
	}
}

func TestBuildQueryResolvesFields(t *testing.T) {
	c, _ := newClient(t)
	ms, err := c.Mappings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var m *homer.Mapping
	for i := range ms {
		if ms[i].Key() == "1_call" {
			m = &ms[i]
		}
	}
	if m == nil || len(m.Fields) == 0 {
		t.Fatal("1_call mapping missing")
	}
	spec := homer.SearchSpec{Key: "1_call", Since: time.Hour, Filters: []homer.Filter{
		{Field: "from_user", Value: "100"}, {Field: "CallID", Value: "x"}, {Field: "srcPort", Value: "5060"}, {Field: "nope", Value: "1"},
	}}
	q, unknown := homer.BuildQuery(spec, m, []string{"localnode"}, time.Now())
	if len(unknown) != 1 || unknown[0] != "nope" {
		t.Fatalf("unknown = %v", unknown)
	}
	got := map[string]string{}
	for _, c := range q.Conditions {
		got[c.Name] = c.Type
	}
	if got["data_header.from_user"] != "string" || got["data_header.callid"] != "string" || got["protocol_header.srcPort"] != "integer" {
		t.Fatalf("conditions = %+v", q.Conditions)
	}
}

func TestSearchTransactionExport(t *testing.T) {
	c, srv := newClient(t)
	ctx := context.Background()
	now := time.Now()
	res, err := c.Search(ctx, homer.Query{Key: "1_call", HepID: 1, Limit: 1000, Nodes: []string{"localnode"},
		From: now.Add(-4 * time.Hour), To: now,
		Conditions: []homer.Condition{{Name: "data_header.method", Value: "INVITE", Type: "string", HepID: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) == 0 {
		t.Fatal("no INVITEs found")
	}
	for _, d := range res.Data {
		if d["method"] != "INVITE" {
			t.Fatalf("filter not applied: %v", d["method"])
		}
	}
	sid := res.Data[0]["sid"].(string)
	ref := homer.CallRef{Key: "1_call", CallIDs: []string{sid}, Nodes: []string{"localnode"}, From: now.Add(-4 * time.Hour), To: now}
	tx, err := c.Transaction(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Data.Calldata) < 4 || len(tx.Data.Calldata) != len(tx.Data.Messages) {
		t.Fatalf("calldata=%d messages=%d", len(tx.Data.Calldata), len(tx.Data.Messages))
	}
	pcap, err := c.ExportPCAP(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcap) < 24 || binary.LittleEndian.Uint32(pcap) != 0xa1b2c3d4 {
		t.Fatalf("bad pcap header, %d bytes", len(pcap))
	}
	txt, err := c.ExportText(ctx, ref)
	if err != nil || !strings.Contains(string(txt), sid) {
		t.Fatalf("text export: %v", err)
	}
	f := filepath.Join(t.TempDir(), "x.pcap")
	if err := os.WriteFile(f, pcap, 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := c.ImportPCAP(ctx, f, true)
	if err != nil || !strings.Contains(msg, "imported") {
		t.Fatalf("import: %q %v", msg, err)
	}
	_ = srv
}

func TestReloginOn401(t *testing.T) {
	c, srv := newClient(t)
	srv.ExpireSessions()
	if _, err := c.Profile(context.Background()); err != nil {
		t.Fatalf("expected transparent re-login, got %v", err)
	}
}

func TestAuthToken(t *testing.T) {
	srv := mock.New(1)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	c := homer.New(homer.Options{URL: ts.URL, AuthToken: srv.Token})
	if _, err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Nodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	bad := homer.New(homer.Options{URL: ts.URL, AuthToken: "wrong"})
	_, err := bad.Nodes(context.Background())
	if !homer.IsUnauthorized(err) || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("expected 401 with a hint about the token, got %v", err)
	}
}

func TestCRUD(t *testing.T) {
	c, srv := newClient(t)
	ctx := context.Background()
	res, _ := homer.FindResource("alias")
	items, err := c.ListRaw(ctx, res.ListPath)
	if err != nil || len(items) != 2 {
		t.Fatalf("list: %d %v", len(items), err)
	}
	if _, err := c.Mutate(ctx, http.MethodPost, res.ListPath, map[string]any{"alias": "x", "ip": "192.0.2.4"}); err != nil {
		t.Fatal(err)
	}
	if srv.Count("/alias") != 3 {
		t.Fatal("create failed")
	}
	key := items[0]["guid"].(string)
	items[0]["alias"] = "renamed"
	if _, err := c.Mutate(ctx, http.MethodPut, res.PathFor(key), items[0]); err != nil {
		t.Fatal(err)
	}
	if srv.Item("/alias", key)["alias"] != "renamed" {
		t.Fatal("update failed")
	}
	if _, err := c.Mutate(ctx, http.MethodDelete, res.PathFor(key), nil); err != nil {
		t.Fatal(err)
	}
	if srv.Count("/alias") != 2 {
		t.Fatal("delete failed")
	}
	for _, r := range homer.Resources {
		if _, err := c.ListRaw(ctx, r.ListPath); err != nil {
			t.Errorf("%s: %v", r.Name, err)
		}
	}
}

// The window of the search form: a duration back from now, or from..to.
func TestParseWindow(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 21, 16, 40, 0, 0, loc)
	var s homer.SearchSpec
	for _, c := range []struct{ in, from, to string }{
		{"1h", "", ""},
		{"15:00..15:30", "2026-10-21 15:00:00", "2026-10-21 15:30:00"},
		{"-3h..-1h", "2026-10-21 13:40:00", "2026-10-21 15:40:00"},
		{"2026-10-20 16:00..", "2026-10-20 16:00:00", ""},
		{"15:00", "2026-10-21 15:00:00", ""},
	} {
		if err := homer.ParseWindow(c.in, &s, loc, now); err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		f, to := "", ""
		if !s.From.IsZero() {
			f = s.From.Format("2006-01-02 15:04:05")
		}
		if !s.To.IsZero() {
			to = s.To.Format("2006-01-02 15:04:05")
		}
		if f != c.from || to != c.to || (c.from == "" && s.Since != time.Hour) {
			t.Fatalf("%s: %q..%q since %v", c.in, f, to, s.Since)
		}
		var again homer.SearchSpec
		if err := homer.ParseWindow(s.Window(loc), &again, loc, now); err != nil || again.From != s.From || again.To != s.To || again.Since != s.Since {
			t.Fatalf("%s: %q reads back as %+v %v", c.in, s.Window(loc), again, err)
		}
	}
	for _, bad := range []string{"", "15:30..15:00", "soon", "15:00..later"} {
		if err := homer.ParseWindow(bad, &s, loc, now); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// The credentials stay with the host they are for: a redirect to another
// host or from https down to http is refused; one on the same host is
// followed. A key from the server stays one segment of the path.
func TestCredentialsStayOnTheHost(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = leaked || r.Header.Get("Auth-Token") != ""
		w.Write([]byte(`{}`))
	}))
	defer other.Close()
	var same *httptest.Server
	same = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/away":
			http.Redirect(w, r, other.URL+"/api/v3/x", http.StatusTemporaryRedirect)
		case "/api/v3/here":
			http.Redirect(w, r, same.URL+"/api/v3/ok", http.StatusTemporaryRedirect)
		case "/api/v3/ok":
			if r.Header.Get("Auth-Token") != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
			}
			w.Write([]byte(`{}`))
		}
	}))
	defer same.Close()
	c := homer.New(homer.Options{URL: same.URL, AuthToken: "secret"})
	if _, err := c.Do(context.Background(), http.MethodGet, "/away", nil); err == nil || !strings.Contains(err.Error(), "refused") || leaked {
		t.Fatalf("a redirect to another host: %v, leaked %v", err, leaked)
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/here", nil); err != nil {
		t.Fatalf("a redirect on the same host: %v", err)
	}
	r := &homer.Resource{ListPath: "/dashboard/info"}
	if got := r.PathFor("../../users/x?y"); got != "/dashboard/info/..%2F..%2Fusers%2Fx%3Fy" {
		t.Fatalf("path %q", got)
	}
	for _, res := range homer.Resources {
		if res.Name == "dashboards" && strings.Contains(res.PathFor("../a"), "../") {
			t.Fatalf("dashboards path %q", res.PathFor("../a"))
		}
	}
}
