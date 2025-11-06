package importer

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"garmr/internal/fitx"
	"garmr/internal/importlog"
	"garmr/internal/store"
)

var ErrDuplicate = errors.New("duplicate activity")

func IngestFile(db *store.DB, rawStore, src string) error {
	if _, err := os.Stat(src); err != nil { return err }

	if err := os.MkdirAll(rawStore, 0o755); err != nil { return err }
	day := time.Now().Format("2006/01/02")
	dstDir := filepath.Join(rawStore, day)
	if err := os.MkdirAll(dstDir, 0o755); err != nil { return err }

	srcF, err := os.Open(src)
	if err != nil { return err }
	defer srcF.Close()

	h := sha1.New()
	tee := io.TeeReader(srcF, h)

	dstPath := filepath.Join(dstDir, filepath.Base(src))
	dstF, err := os.Create(dstPath)
	if err != nil { return err }
	if _, err := io.Copy(dstF, tee); err != nil { dstF.Close(); return err }
	dstF.Close()
	hash := hex.EncodeToString(h.Sum(nil))

	act, recs, laps, zones, err := fitx.ParseFIT(dstPath)
	if err != nil { return err }

	return db.WithTx(func(tx *sql.Tx) error {
		if act.FitUID != "" {
			if _, err := db.LookupActivityByUID(tx, act.FitUID); err == nil {
				importlog.Printf("importer: skip duplicate (uid) %s", src)
				return ErrDuplicate
			}
		}
		if _, err := db.LookupActivityByHash(tx, hash); err == nil {
			importlog.Printf("importer: skip duplicate (hash) %s", src)
			return ErrDuplicate
		}

		id, err := db.InsertActivity(tx, act, dstPath, hash)
		if err != nil { return err }
		if err := db.InsertRecords(tx, id, recs); err != nil { return err }
		if err := db.InsertLaps(tx, id, laps); err != nil { return err }

		// Insert HR zones if available
		if len(zones) > 0 {
			var storeZones []store.HRZone
			for _, z := range zones {
				storeZones = append(storeZones, store.HRZone{
					Zone:        z.Zone,
					TimeSeconds: z.TimeSeconds,
				})
			}
			if err := db.InsertHRZones(tx, id, storeZones); err != nil { return err }
		}

		if err := db.UpsertDailyAgg(tx, act.StartTimeUTC, act); err != nil { return err }

		importlog.Printf("importer: imported id=%d from %s (%s %dm %ds)", id, src, act.Sport, act.DistanceM, act.DurationS)
		return nil
	})
}
