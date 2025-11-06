package importer

import (
	"context"
	"time"

	"garmr/internal/cfg"
	"garmr/internal/importlog"
	"garmr/internal/store"
)

type Importer struct {
	c  cfg.Config
	db *store.DB
}

func New(c cfg.Config, db *store.DB) *Importer {
	return &Importer{c: c, db: db}
}

func (im *Importer) Run(ctx context.Context) {
	if im.c.PollMs <= 0 {
		importlog.Printf("importer: background polling disabled (poll_ms=%d)", im.c.PollMs)
		return
	}
	importlog.Printf("importer: polling every %dms", im.c.PollMs)

	t := time.NewTicker(time.Duration(im.c.PollMs) * time.Millisecond)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = im.ScanOnce()
		}
	}
}
