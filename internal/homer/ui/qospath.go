package ui

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// mediaOwner is who announced a media address in SDP: the SIP host, and
// its place in the call, the caller first.
type mediaOwner struct {
	name string
	pos  int
}

// mediaOwners maps media addresses to the hosts that announced them;
// "ip:" and an IP stand for any port of it.
type mediaOwners map[string]mediaOwner

// of finds the owner of an address, by the IP when the port was never
// announced: a host of the call on that IP.
func (o mediaOwners) of(addr string) (mediaOwner, bool) {
	if w, ok := o[addr]; ok {
		return w, true
	}
	ip, _, _ := strings.Cut(addr, ":")
	w, ok := o["ip:"+ip]
	return w, ok
}

// mediaPlan is what the SIP of a call says about its media: who owns
// each media address, the hops its offers and answers set up, and the
// hosts of the call. Lanes are those of the flow by alias and unfolded, so
// a call that comes back through the same hosts has lanes of its own, and
// a relay with two addresses under one alias is one lane.
type mediaPlan struct {
	owners mediaOwners
	hops   [][2]string // the RTCP addresses of both ends of a hop
	hosts  []planHost  // by lane; no name on a lane without SIP
}

type planHost struct{ name, ip string }

// mediaPlan reads the SDP of the call's SIP messages: the address of each
// m= section, the session's c= or its own, and its RTCP port, the next one
// or a=rtcp, belong to the lane of the host that sent it; the first
// address each side of a dialog hop sends makes a media hop.
func (v *CallView) mediaPlan() *mediaPlan {
	plan := &mediaPlan{owners: mediaOwners{}}
	if v.tx == nil {
		return plan
	}
	saved := v.vis
	v.vis = make([]int, len(v.tx.Data.Calldata))
	for i := range v.vis {
		v.vis[i] = i
	}
	hosts, arrows := v.layout(true, true)
	v.vis = saved
	plan.hosts = make([]planHost, len(hosts))
	name := func(lane int, id, ip string) string {
		if l := hosts[lane].label; l != id {
			return l
		}
		return ip
	}
	own := func(key, name string, lane int) {
		if _, ok := plan.owners[key]; !ok {
			plan.owners[key] = mediaOwner{name, lane}
		}
	}
	type side struct {
		lane int
		addr string
	}
	offers := map[string][]side{} // dialog hop → the first media of each side
	var order []string
	for i, c := range v.tx.Data.Calldata {
		m := v.message(i)
		if msgKind(m, c.Method) != "SIP" {
			continue
		}
		src, dst := arrows[i][0], arrows[i][1]
		srcName := name(src, c.SrcID, c.SrcIP)
		for _, e := range []struct {
			lane     int
			name, ip string
		}{{src, srcName, c.SrcIP}, {dst, name(dst, c.DstID, c.DstIP), c.DstIP}} {
			if plan.hosts[e.lane].name == "" {
				plan.hosts[e.lane] = planHost{e.name, e.ip}
			}
			own("ip:"+e.ip, e.name, e.lane)
		}
		for n, sec := range sdpSections(parseSIP(str(m, "raw")).body) {
			rtcp := fmt.Sprintf("%s:%d", sec.rtcpAddr, sec.rtcpPort)
			own(fmt.Sprintf("%s:%d", sec.addr, sec.port), srcName, src)
			own(rtcp, srcName, src)
			own("ip:"+sec.addr, srcName, src)
			if n > 0 { // the first stream, the audio, is the hop
				continue
			}
			key := fmt.Sprintf("%s|%d|%d", c.Sid, min(src, dst), max(src, dst))
			sides := offers[key]
			if len(sides) == 0 {
				order = append(order, key)
			}
			if len(sides) < 2 && (len(sides) == 0 || sides[0].lane != src) {
				offers[key] = append(sides, side{src, rtcp})
			}
		}
	}
	for _, key := range order {
		if sides := offers[key]; len(sides) == 2 {
			plan.hops = append(plan.hops, [2]string{sides[0].addr, sides[1].addr})
		}
	}
	return plan
}

type sdpSection struct {
	addr, rtcpAddr string
	port, rtcpPort int
}

// sdpSections are the m= sections of an SDP body with their addresses.
func sdpSections(body string) []sdpSection {
	session := ""
	var out []sdpSection
	var cur *sdpSection
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		k, val, _ := strings.Cut(strings.TrimSpace(l), "=")
		f := strings.Fields(val)
		switch {
		case k == "m" && len(f) >= 2:
			port, err := strconv.Atoi(f[1])
			if err != nil || port == 0 {
				cur = nil
				continue
			}
			out = append(out, sdpSection{port: port})
			cur = &out[len(out)-1]
		case k == "c" && len(f) >= 3:
			if cur == nil {
				session = f[2]
			} else {
				cur.addr = f[2]
			}
		case k == "a" && cur != nil && strings.HasPrefix(val, "rtcp:"):
			r := strings.Fields(strings.TrimPrefix(val, "rtcp:"))
			if len(r) == 0 {
				continue
			}
			if p, err := strconv.Atoi(r[0]); err == nil {
				cur.rtcpPort = p
			}
			if len(r) >= 4 {
				cur.rtcpAddr = r[3]
			}
		}
	}
	keep := out[:0]
	for _, s := range out {
		if s.addr == "" {
			s.addr = session
		}
		if s.addr == "" {
			continue
		}
		if s.rtcpPort == 0 {
			s.rtcpPort = s.port + 1
		}
		if s.rtcpAddr == "" {
			s.rtcpAddr = s.addr
		}
		keep = append(keep, s)
	}
	return keep
}

// qosDir is the media one way between two addresses, all its SSRCs
// together: a re-INVITE or a restart changes the SSRC, not the way.
type qosDir struct {
	from, to string
	points   []qosPoint
	rates    []qosRate
}

// qosLeg is a media hop: a nearer the caller, b nearer the callee.
type qosLeg struct {
	a, b       string
	pos        int
	fwd, back  *qosDir // a → b, to the callee; b → a, to the caller
	aPos, bPos int
}

// qosLegs puts the hops of the plan and the RTCP streams on the media
// path, in the order of the call. A hop is a pair of lanes, so a stream
// from another port of the same hosts, after a re-INVITE, joins its hop;
// an address no SIP announced is a hop of its own.
func qosLegs(streams []*qosStream, plan *mediaPlan) []*qosLeg {
	unknown := map[string]int{}
	posOf := func(addr string) int {
		if o, ok := plan.owners.of(addr); ok {
			return o.pos
		}
		if _, ok := unknown[addr]; !ok {
			unknown[addr] = 1000 + len(unknown)
		}
		return unknown[addr]
	}
	legs := map[string]*qosLeg{}
	var order []*qosLeg
	leg := func(a, b string) (*qosLeg, bool) {
		pa, pb := posOf(a), posOf(b)
		left := pa < pb || pa == pb && a < b
		if !left {
			a, b, pa, pb = b, a, pb, pa
		}
		key := a + "|" + b
		if pa < 1000 && pb < 1000 {
			key = fmt.Sprintf("%d|%d", pa, pb)
		}
		l := legs[key]
		if l == nil {
			l = &qosLeg{a: a, b: b, pos: pa, aPos: pa, bPos: pb}
			legs[key] = l
			order = append(order, l)
		}
		return l, left
	}
	for _, h := range plan.hops {
		leg(h[0], h[1])
	}
	for _, s := range streams {
		l, fwd := leg(s.from, s.to)
		// an end the SDP left at 0.0.0.0, ICE or hold, takes the address
		// the reports come from
		aSide, bSide := s.from, s.to
		if !fwd {
			aSide, bSide = s.to, s.from
		}
		if strings.HasPrefix(l.a, "0.0.0.0:") {
			l.a = aSide
		}
		if strings.HasPrefix(l.b, "0.0.0.0:") {
			l.b = bSide
		}
		d := &l.back
		if fwd {
			d = &l.fwd
		}
		if *d == nil {
			*d = &qosDir{from: s.from, to: s.to}
		}
		(*d).points = append((*d).points, s.points...)
		(*d).rates = append((*d).rates, s.rates...)
	}
	for _, l := range order {
		for _, d := range []*qosDir{l.fwd, l.back} {
			if d == nil {
				continue
			}
			sort.SliceStable(d.points, func(i, j int) bool { return d.points[i].t.Before(d.points[j].t) })
			sort.SliceStable(d.rates, func(i, j int) bool { return d.rates[i].t.Before(d.rates[j].t) })
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].aPos != order[j].aPos {
			return order[i].aPos < order[j].aPos
		}
		return order[i].bPos < order[j].bPos
	})
	return order
}

// dirQuality sums up one way of a hop.
type dirQuality struct {
	grade                  int // 0 good, 1 fair, 2 poor, -1 no reports
	mos, loss, jitter, rtt float64
	avgJitter              float64
	lost                   int64
	firstBad, silentFrom   time.Time
	hasRTT, silent         bool
}

func (d *qosDir) quality() dirQuality {
	q := dirQuality{grade: -1, mos: 5}
	if d == nil {
		return q
	}
	for _, r := range d.rates {
		if r.kbps < 1 && !q.silent {
			q.silent, q.silentFrom = true, r.t
		}
	}
	if len(d.points) == 0 {
		return q
	}
	q.grade = 0
	sum := 0.0
	for _, p := range d.points {
		q.mos = math.Min(q.mos, mosOf(p))
		q.loss = math.Max(q.loss, p.loss)
		q.jitter = math.Max(q.jitter, p.jitter)
		sum += p.jitter
		q.lost = max(q.lost, p.lost)
		if p.hasRTT {
			q.rtt, q.hasRTT = math.Max(q.rtt, p.rtt), true
		}
		g := 0
		switch {
		case p.loss >= 5 || p.jitter >= 50 || mosOf(p) < 3.1:
			g = 2
		case p.loss >= 1 || p.jitter >= 20 || mosOf(p) < 3.6:
			g = 1
		}
		if g > 0 && q.firstBad.IsZero() {
			q.firstBad = p.t
		}
		q.grade = max(q.grade, g)
	}
	q.avgJitter = sum / float64(len(d.points))
	return q
}

func gradeMark(g int) string {
	switch g {
	case 0:
		return theme.Tag(theme.OK) + "● " + T("good") + "[-]"
	case 1:
		return theme.Tag(theme.Warn) + "● " + T("fair") + "[-]"
	case 2:
		return theme.Tag(theme.Err) + "● " + T("poor") + "[-]"
	}
	return theme.Tag(theme.Dim) + "○ " + T("no reports") + "[-]"
}

// ownerName is the host that announced an address, else the address.
func ownerName(owners mediaOwners, addr string) string {
	if o, ok := owners.of(addr); ok {
		return o.name
	}
	return addr
}

// pathNode is a column of the scheme: a host and the IPs it has on the
// hops next to it, two for a relay with an inside and an outside address.
type pathNode struct {
	name string
	ips  []string
}

func (n *pathNode) addIP(addr string) {
	ip, _, _ := strings.Cut(addr, ":")
	if ip != "0.0.0.0" && !slices.Contains(n.ips, ip) {
		n.ips = append(n.ips, ip)
	}
}

// pathNodes makes a column of every host of the call in the order of its
// lane, the caller first, with or without media: a proxy the sound does
// not pass shows the arrows over it, as the flow does. A lane with two
// addresses, a relay, shows both IPs. An address no SIP announced gets a
// column at the end. It returns the columns of each hop.
func pathNodes(legs []*qosLeg, plan *mediaPlan) ([]*pathNode, [][2]int) {
	var lanes []int
	for lane, h := range plan.hosts {
		if h.name != "" {
			lanes = append(lanes, lane)
		}
	}
	for _, l := range legs {
		lanes = append(lanes, l.aPos, l.bPos)
	}
	slices.Sort(lanes)
	lanes = slices.Compact(lanes)
	nodes := make([]*pathNode, len(lanes))
	for k, lane := range lanes {
		nodes[k] = &pathNode{}
		if lane < len(plan.hosts) {
			nodes[k].name = plan.hosts[lane].name
		}
	}
	col := func(pos int, addr string) int {
		k, _ := slices.BinarySearch(lanes, pos)
		if nodes[k].name == "" {
			nodes[k].name = ownerName(plan.owners, addr)
		}
		nodes[k].addIP(addr)
		return k
	}
	cols := make([][2]int, len(legs))
	for i, l := range legs {
		cols[i] = [2]int{col(l.aPos, l.a), col(l.bPos, l.b)}
	}
	for k, lane := range lanes { // no media here: the SIP address
		if len(nodes[k].ips) == 0 && lane < len(plan.hosts) {
			nodes[k].ips = []string{plan.hosts[lane].ip}
		}
	}
	return nodes, cols
}

// canvas is a line of runes, each with its colour.
type canvas struct {
	r []rune
	c []tcell.Color
}

func newCanvas(w int) *canvas {
	c := &canvas{r: []rune(strings.Repeat(" ", w)), c: make([]tcell.Color, w)}
	for i := range c.c {
		c.c[i] = theme.Dim
	}
	return c
}

func (c *canvas) put(x int, s string, col tcell.Color) {
	for _, r := range s {
		if x >= 0 && x < len(c.r) {
			c.r[x], c.c[x] = r, col
		}
		x++
	}
}

func (c *canvas) String() string {
	var b strings.Builder
	for i := 0; i < len(c.r); {
		j := i
		for j < len(c.r) && c.c[j] == c.c[i] {
			j++
		}
		b.WriteString(theme.Tag(c.c[i]) + tview.Escape(string(c.r[i:j])))
		i = j
	}
	return strings.TrimRight(b.String(), " ") + "[-]"
}

func gradeColor(g int) tcell.Color {
	switch g {
	case 0:
		return theme.OK
	case 1:
		return theme.Warn
	case 2:
		return theme.Err
	}
	return theme.Dim
}

// cellText is what an arrow of the scheme says: MOS and the loss, or that
// nobody reported.
func cellText(q dirQuality) string {
	if q.grade < 0 {
		if q.silent {
			return T("no media")
		}
		return T("no data")
	}
	s := fmt.Sprintf("● %.1f", q.mos)
	if q.loss >= 0.1 {
		s += fmt.Sprintf(" %.1f%%", q.loss)
	}
	if q.silent {
		s += " " + T("no media")
	}
	return s
}

// hears says how one side hears the other: from the report of the hop
// next to it, else the nearest hop reported on that way.
func hears(legs []*qosLeg, toCallee bool, owners mediaOwners) string {
	order := make([]int, len(legs))
	for i := range legs {
		order[i] = i
		if toCallee {
			order[i] = len(legs) - 1 - i
		}
	}
	for n, i := range order {
		l := legs[i]
		d := l.back
		if toCallee {
			d = l.fwd
		}
		q := d.quality()
		if q.grade < 0 && !q.silent {
			continue
		}
		s := gradeMark(q.grade)
		if q.grade >= 0 {
			s += "  " + Tf("MOS %.1f, loss %.1f%% (%d), jitter %.1f/%.1f ms", q.mos, q.loss, q.lost, q.avgJitter, q.jitter)
		}
		if q.silent {
			s += "  " + theme.Tag(theme.Err) + T("no media") + "[-]"
		}
		if n == 0 {
			return s + "  " + tui.Faint(Tf("reported by %s", tview.Escape(ownerName(owners, d.to))))
		}
		return tui.Faint(T("no report from its side; nearest:")+" "+tview.Escape(ownerName(owners, d.from))+" → "+tview.Escape(ownerName(owners, d.to))) + "  " + s
	}
	return tui.Faint(T("no reports on this way"))
}

// qosPath draws the media of a call: the hosts in a row, the sound to the
// callee and to the caller under them, what each side hears and where it
// goes bad, then the quality of each reported hop over time.
func qosPath(legs []*qosLeg, plan *mediaPlan, width int, loc *time.Location) string {
	owners := plan.owners
	if len(legs) == 0 {
		return tui.Faint(T("no RTCP reports for this call"))
	}
	nodes, cols := pathNodes(legs, plan)
	rowLabels := []string{T("callee hears"), T("caller hears")}
	labelW := 0
	for _, l := range rowLabels {
		labelW = max(labelW, utf8.RuneCountInString(l))
	}
	labelW += 2
	colW := 20
	for _, n := range nodes {
		colW = max(colW, utf8.RuneCountInString(n.name)+2, len(strings.Join(n.ips, " / "))+2)
	}
	for i, l := range legs {
		span := max(1, cols[i][1]-cols[i][0])
		for _, d := range []*qosDir{l.fwd, l.back} {
			colW = max(colW, (utf8.RuneCountInString(cellText(d.quality()))+8+span-1)/span)
		}
	}
	at := func(k int) int { return labelW + colW/2 + k*colW }
	lineW := at(len(nodes)-1) + colW/2 + 1
	var b strings.Builder
	names, ips := newCanvas(lineW), newCanvas(lineW)
	for k, n := range nodes {
		r := []rune(n.name)
		if len(r) > colW-1 {
			r = append(r[:colW-2], '…')
		}
		names.put(at(k)-len(r)/2, string(r), theme.InfoKey)
		if ip := strings.Join(n.ips, " / "); ip != n.name {
			ips.put(at(k)-len(ip)/2, ip, theme.Dim)
		}
	}
	b.WriteString("[::b]" + names.String() + "[::-]\n" + ips.String() + "\n")
	for row, toCallee := range []bool{true, false} {
		c := newCanvas(lineW)
		c.put(0, rowLabels[row], theme.Fg)
		for k := range nodes {
			c.put(at(k), "│", theme.Dim)
		}
		for i, l := range legs {
			d := l.back
			if toCallee {
				d = l.fwd
			}
			q := d.quality()
			col := gradeColor(q.grade)
			if q.silent {
				col = theme.Err
			}
			x0, x1 := at(cols[i][0]), at(cols[i][1])
			if x1 <= x0 { // both ends on one lane: nothing to draw between
				continue
			}
			c.put(x0+1, strings.Repeat("─", x1-x0-1), col)
			if toCallee {
				c.put(x1-1, "▶", col)
			} else {
				c.put(x0+1, "◀", col)
			}
			text := " " + cellText(q) + " "
			c.put(x0+1+(x1-x0-1-utf8.RuneCountInString(text))/2, text, col)
		}
		b.WriteString(c.String() + "\n")
	}

	fmt.Fprintf(&b, "\n%s[::b]%s[::-][-]\n", theme.Tag(theme.Title), T("Summary"))
	sideW := max(utf8.RuneCountInString(T("caller hears")), utf8.RuneCountInString(T("callee hears")), utf8.RuneCountInString(T("trouble")))
	fmt.Fprintf(&b, "  %-*s  %s\n", sideW, T("caller hears"), hears(legs, false, owners))
	fmt.Fprintf(&b, "  %-*s  %s\n", sideW, T("callee hears"), hears(legs, true, owners))
	name := func(addr string) string { return tview.Escape(ownerName(owners, addr)) }
	toCallee, badCallee, blindCallee := where(legs, true, name, loc)
	toCaller, badCaller, blindCaller := where(legs, false, name, loc)
	reported := false
	for _, l := range legs {
		for _, d := range []*qosDir{l.fwd, l.back} {
			reported = reported || d != nil && len(d.points) > 0
		}
	}
	if !reported {
		fmt.Fprintf(&b, "  %-*s  %s\n", sideW, T("trouble"), tui.Faint(T("no RTCP reports, nothing to judge by")))
	} else if badCallee || badCaller {
		fmt.Fprintf(&b, "  %-*s  → %s\n", sideW, T("trouble"), toCallee)
		fmt.Fprintf(&b, "  %-*s  ← %s\n", sideW, "", toCaller)
	} else {
		line := theme.Tag(theme.OK) + T("none seen") + "[-]"
		var blind []string
		if len(blindCallee) > 0 {
			blind = append(blind, "→ "+strings.Join(blindCallee, ", "))
		}
		if len(blindCaller) > 0 {
			blind = append(blind, "← "+strings.Join(blindCaller, ", "))
		}
		if len(blind) > 0 {
			line += "  " + tui.Faint(Tf("hops without reports: %s", strings.Join(blind, "; ")))
		}
		fmt.Fprintf(&b, "  %-*s  %s\n", sideW, T("trouble"), line)
	}

	// the quality of each reported way over the call
	type series struct {
		label string
		d     *qosDir
	}
	var all []series
	var t0, t1 time.Time
	var steps []time.Duration
	for i, l := range legs {
		for _, d := range []*qosDir{l.fwd, l.back} {
			if d == nil || len(d.points) == 0 {
				continue
			}
			all = append(all, series{fmt.Sprintf("%d %s → %s", i+1, ownerName(owners, d.from), ownerName(owners, d.to)), d})
			for k, p := range d.points {
				if t0.IsZero() || p.t.Before(t0) {
					t0 = p.t
				}
				t1 = maxTime(t1, p.t)
				if k > 0 {
					steps = append(steps, p.t.Sub(d.points[k-1].t))
				}
			}
		}
	}
	if len(all) == 0 {
		return b.String()
	}
	gap := t1.Sub(t0)
	if len(steps) > 0 {
		sort.Slice(steps, func(i, j int) bool { return steps[i] < steps[j] })
		gap = steps[len(steps)/2] * 3 / 2
	}
	serW := 0
	for _, s := range all {
		serW = max(serW, utf8.RuneCountInString(s.label))
	}
	graphW := max(10, width-serW-4-34)
	start, end := t0.In(loc).Format("15:04:05"), t1.In(loc).Format("15:04:05")
	fmt.Fprintf(&b, "\n%s[::b]%s[::-][-]  %s\n", theme.Tag(theme.Title), T("Over time"), tui.Faint(T("MOS of each reported hop along the call: green good, yellow fair, red poor")))
	fmt.Fprintf(&b, "  %s  %s\n", strings.Repeat(" ", serW), tui.Faint(start+strings.Repeat(" ", max(1, graphW-len(start)-len(end)))+end))
	for _, s := range all {
		q := s.d.quality()
		mos := &metric{worst: lower, warn: mosShade}
		for _, p := range s.d.points {
			mos.add(p.t, mosOf(p))
		}
		nums := fmt.Sprintf("%s %s%.1f%%[-]  %s %.1f/%s%.1f[-]", T("loss"), theme.Tag(above(1, 5)(q.loss)), q.loss,
			T("jitter"), q.avgJitter, theme.Tag(above(20, 50)(q.jitter)), q.jitter)
		if q.hasRTT {
			nums += fmt.Sprintf("  RTT %.0f", q.rtt)
		}
		fmt.Fprintf(&b, "  %s%-*s[-]  %s  %s\n", theme.Tag(theme.InfoKey), serW, tview.Escape(s.label), mos.line(t0, t1, graphW, gap), nums)
	}
	return b.String()
}

// qosHops lists the hops with their addresses, for the pane under the
// graphs.
func qosHops(legs []*qosLeg, owners mediaOwners) string {
	var b strings.Builder
	b.WriteString(T("Hops") + "\n")
	for i, l := range legs {
		fmt.Fprintf(&b, "  %d  %s %s  ⇄  %s %s\n", i+1, tview.Escape(ownerName(owners, l.a)), tui.Faint(l.a), tview.Escape(ownerName(owners, l.b)), tui.Faint(l.b))
	}
	return b.String()
}

// where finds the first hop on the way of the sound where it goes bad:
// what is wrong further on usually comes from there. bad tells trouble
// from a clean way; blind are the hops of the way nobody reported on.
func where(legs []*qosLeg, fwd bool, name func(string) string, loc *time.Location) (text string, bad bool, blind []string) {
	order := append([]*qosLeg(nil), legs...)
	if !fwd {
		slices.Reverse(order)
	}
	seen := false
	for _, l := range order {
		d := l.fwd
		if !fwd {
			d = l.back
		}
		q := d.quality()
		if q.silent {
			return theme.Tag(theme.Err) + Tf("no media on hop %d (%s → %s) since %s", legNo(legs, l), name(d.from), name(d.to), q.silentFrom.In(loc).Format("15:04:05")) + "[-]", true, blind
		}
		if q.grade < 0 {
			blind = append(blind, strconv.Itoa(legNo(legs, l)))
			continue
		}
		seen = true
		if q.grade > 0 {
			var what []string
			if q.loss >= 1 {
				what = append(what, Tf("loss up to %.1f%%", q.loss))
			}
			if q.jitter >= 20 {
				what = append(what, Tf("jitter up to %.0f ms", q.jitter))
			}
			if len(what) == 0 {
				what = append(what, Tf("MOS down to %.1f", q.mos))
			}
			text = theme.Tag(theme.Warn) + Tf("%s from %s, first on hop %d (%s → %s)", strings.Join(what, ", "), q.firstBad.In(loc).Format("15:04:05"),
				legNo(legs, l), name(d.from), name(d.to)) + "[-]"
			if len(blind) > 0 {
				text += tui.Faint("; " + Tf("no reports on hop %s before it", strings.Join(blind, ", ")))
			}
			return text, true, blind
		}
	}
	if !seen {
		return tui.Faint(T("no reports")), false, blind
	}
	if len(blind) > 0 {
		return theme.Tag(theme.OK) + T("clean where reported") + "[-]" + tui.Faint("; "+Tf("no reports on hop %s", strings.Join(blind, ", "))), false, blind
	}
	return theme.Tag(theme.OK) + T("clean on every hop") + "[-]", false, blind
}

func legNo(legs []*qosLeg, l *qosLeg) int {
	for i, x := range legs {
		if x == l {
			return i + 1
		}
	}
	return 0
}

// mosShade colours MOS from green to red in five steps, for the line of
// a hop over time.
func mosShade(v float64) tcell.Color {
	switch {
	case v >= 4.2:
		return theme.OK
	case v >= 4.0:
		return tcell.ColorYellowGreen
	case v >= 3.6:
		return tcell.ColorGold
	case v >= 3.1:
		return tcell.ColorDarkOrange
	}
	return theme.Err
}
