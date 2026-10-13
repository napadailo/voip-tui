package mock

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// WriteLog makes the server keep a log file at path laid out like
// rtpengine's under journald (journalctl -o short-iso-precise): the signalling
// of every call, terminations and debug switches. The calls present now get
// their offer and answer lines.
func (s *Server) WriteLog(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return err
	}
	s.logFile = path
	calls := make([]*call, 0, len(s.calls))
	for _, c := range s.calls {
		calls = append(calls, c)
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].created.Before(calls[j].created) })
	for _, c := range calls {
		s.logCall(c)
	}
	return nil
}

// logCall writes the offer and answer of a new call. It must not draw from
// s.rnd: the generated calls would differ with and without a log.
func (s *Server) logCall(c *call) {
	elapsed := 200 + int(c.created.UnixMicro()%700)
	s.logf(c.created, "INFO", c.id, "Received command 'offer' from %s", c.proxy)
	s.logf(c.created, "INFO", c.id, "Replying to 'offer' from %s (elapsed time 0.000%03d sec)", c.proxy, elapsed)
	answered := c.created.Add(2 * time.Second)
	s.logf(answered, "INFO", c.id, "Received command 'answer' from %s", c.proxy)
	s.logf(answered, "INFO", c.id, "Replying to 'answer' from %s (elapsed time 0.000%03d sec)", c.proxy, elapsed+31)
}

func (s *Server) logf(at time.Time, level, id, format string, args ...any) {
	if s.logFile == "" {
		return
	}
	f, err := os.OpenFile(s.logFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s rtpe-mock rtpengine[4242]: %s: [%s]: %s\n", at.Format(logTimeLayout), level, id, fmt.Sprintf(format, args...))
}

// logTimeLayout is the time of journalctl -o short-iso-precise.
const logTimeLayout = "2006-01-02T15:04:05.000000-0700"
