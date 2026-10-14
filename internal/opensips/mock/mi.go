package mock

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/internal/opensips/config"
)

// The MI transports of OpenSIPS 3.x as the emulator answers them: mi_fifo
// (the request ":<reply fifo>:<json>" written into the fifo, the reply in the
// reply fifo) and mi_http (JSON-RPC POSTed to http://<ip>:8888/mi). Both take
// JSON-RPC 2.0 and need 3.0+; 2.x reads another fifo format and never replies.

// loaded reports whether a module is loaded; MI transports are unless left out.
func (h *Host) loaded(module string) bool {
	for _, w := range h.Without {
		if w == module {
			return false
		}
	}
	return true
}

// rpcAnswer answers a JSON-RPC request.
func (h *Host) rpcAnswer(body []byte) []byte {
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		ID     json.RawMessage `json:"id"`
	}
	rep := map[string]any{"jsonrpc": "2.0"}
	if err := json.Unmarshal(body, &req); err != nil || req.Method == "" {
		rep["id"], rep["error"] = nil, map[string]any{"code": -32700, "message": "Parse error"}
		out, _ := json.Marshal(rep)
		return out
	}
	rep["id"] = req.ID
	args, ok := rpcArgs(req.Params)
	if !ok {
		rep["error"] = map[string]any{"code": -32602, "message": "Invalid params"}
	} else if ans, err := h.Call(req.Method, args); err != nil {
		var f *Failure
		if errors.As(err, &f) {
			rep["error"] = map[string]any{"code": f.Code, "message": f.Message}
		} else {
			rep["error"] = map[string]any{"code": 500, "message": err.Error()}
		}
	} else {
		rep["result"] = ans
	}
	out, _ := json.Marshal(rep)
	return out
}

// rpcArgs turns JSON-RPC params into the k=v and positional words of Call.
func rpcArgs(raw json.RawMessage) ([]string, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		return nil, true
	}
	var pos []any
	if json.Unmarshal(raw, &pos) == nil {
		out := make([]string, len(pos))
		for i, v := range pos {
			out[i] = fmt.Sprint(v)
		}
		return out, true
	}
	var named map[string]any
	if json.Unmarshal(raw, &named) != nil {
		return nil, false
	}
	keys := make([]string, 0, len(named))
	for k := range named {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + fmt.Sprint(named[k])
	}
	return out, true
}

// fifo runs the mi_fifo script of the client: fifo name, reply dir, request.
func (h *Host) fifo(fifo, dir, req string) opensips.Result {
	if !h.loaded("mi_fifo") || fifo != config.DefaultFIFO {
		return opensips.Result{RC: 3, Err: "no MI fifo at " + fifo + ": is mi_fifo loaded with this fifo_name?\n"}
	}
	if h.version().Less(opensips.MinCLIVersion) || strings.TrimSuffix(dir, "/") != strings.TrimSuffix(config.DefaultReplyDir, "/") {
		// OpenSIPS writes no reply where the client waits: it would hang until the timeout
		return opensips.Result{RC: 124, Err: "no reply in " + dir + "\n"}
	}
	return opensips.Result{Out: string(h.rpcAnswer([]byte(req))) + "\n"}
}

// Dial is ssh -W to mi_http as seen from the host.
func (t *Transport) Dial(ctx context.Context, addr string) (net.Conn, error) {
	if t.spec.Unreachable {
		return nil, &opensips.Unreachable{Msg: "ssh: connect to host " + t.target + " port 22: Connection refused"}
	}
	return t.dialHTTP(addr, addr == "127.0.0.1:8888" || addr == "localhost:8888")
}

// DialDirect connects to mi_http from here (via: direct): any address on
// port 8888, the httpd default, or on the port of DemoListen.
func (t *Transport) DialDirect(ctx context.Context, addr string) (net.Conn, error) {
	_, port, _ := net.SplitHostPort(addr)
	return t.dialHTTP(addr, !t.spec.Unreachable && (port == "8888" || port == "18888"))
}

func (t *Transport) dialHTTP(addr string, listening bool) (net.Conn, error) {
	if !listening || !t.host.loaded("mi_http") {
		return nil, fmt.Errorf("connect to %s: connection refused", addr)
	}
	client, server := net.Pipe()
	go t.host.serveHTTP(server)
	return client, nil
}

// serveHTTP answers one request on conn like mi_http with its root "mi".
func (h *Host) serveHTTP(conn net.Conn) {
	defer conn.Close()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	body, _ := io.ReadAll(req.Body)
	resp := &http.Response{StatusCode: http.StatusOK, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{}, Close: true}
	var out []byte
	switch {
	case req.URL.Path != "/mi":
		resp.StatusCode, out = http.StatusNotFound, []byte("Not Found")
	case req.Method != http.MethodPost:
		resp.StatusCode, out = http.StatusMethodNotAllowed, []byte("Method Not Allowed")
	case h.version().Less(opensips.MinCLIVersion):
		resp.StatusCode, out = http.StatusNotFound, []byte("Not Found") // 2.x mi_http has no JSON-RPC
	default:
		out = h.rpcAnswer(body)
		resp.Header.Set("Content-Type", "application/json")
	}
	resp.Status = http.StatusText(resp.StatusCode)
	resp.ContentLength = int64(len(out))
	resp.Body = io.NopCloser(bytes.NewReader(out))
	_ = resp.Write(conn)
}
