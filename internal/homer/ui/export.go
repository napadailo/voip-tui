package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
)

// exportRows downloads the sessions as pcap or text into the download dir.
func exportRows(app *App, ref *homer.CallRef, kind string) {
	if ref == nil || len(ref.CallIDs) == 0 {
		app.Flash().Warn(T("nothing selected"))
		return
	}
	r := *ref
	name := sanitizeFileName(r.CallIDs[0])
	if len(r.CallIDs) > 1 {
		name += fmt.Sprintf("+%d", len(r.CallIDs)-1)
	}
	ext := ".pcap"
	if kind == "text" {
		ext = ".txt"
	}
	path := filepath.Join(app.cfg.DownloadDir, "homer-"+name+"-"+time.Now().Format("20060102-150405")+ext)
	app.Flash().Infof("exporting %d session(s) as %s…", len(r.CallIDs), kind)
	app.Async(func(ctx context.Context) func() {
		var data []byte
		var err error
		if kind == "text" {
			data, err = app.client.ExportText(ctx, r)
		} else {
			data, err = app.client.ExportPCAP(ctx, r)
		}
		if err == nil {
			path, err = tui.WriteNew(path, data)
		}
		return func() {
			if err != nil {
				app.Flash().Err(err)
				return
			}
			if kind == "pcap" && len(data) <= 24 {
				app.Flash().Warnf("%s saved, but it has no packets (check the time window)", path)
				return
			}
			app.Flash().Infof("saved %s (%s)", path, humanBytes(len(data)))
		}
	})
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
