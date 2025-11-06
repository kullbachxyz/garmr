package fitx

import (
	"os"
	"time"

	"github.com/tormoder/fit"
)

const semicirclesToDeg = 180.0 / 2147483648.0 // 2^31

type Activity struct {
	FitUID       string
	StartTimeUTC time.Time
	Sport        string
	SubSport     string
	DurationS    int
	DistanceM    int
	AvgHR        int
	MaxHR        int
	AvgSpeedMPS  float64
	Calories     int
	AscentM      float64
	DescentM     float64
	DeviceVendor string
	DeviceModel  string
	// Training effects (Garmin specific)
	AerobicTE   *float64 // Aerobic Training Effect (0.0-5.0)
	AnaerobicTE *float64 // Anaerobic Training Effect (0.0-5.0)
}

type Record struct {
	TOffsetS int
	Lat      *float64
	Lon      *float64
	ElevM    *float64
	HR       *int
	Cad      *int
	TempC    *float64
	PowerW   *int
	SpeedMPS *float64
}

type Lap struct {
	Index    int
	StartOff int
	DurS     int
	DistM    int
	AvgHR    int
	MaxHR    int
	AvgSpd   float64
}

type HRZone struct {
	Zone        int
	TimeSeconds int
}

func ParseFIT(path string) (Activity, []Record, []Lap, []HRZone, error) {
	f, err := os.Open(path)
	if err != nil { return Activity{}, nil, nil, nil, err }
	defer f.Close()

	fd, err := fit.Decode(f)
	if err != nil { return Activity{}, nil, nil, nil, err }

	af, err := fd.Activity()
	if err != nil { return Activity{}, nil, nil, nil, err }
	if len(af.Sessions) == 0 {
		return Activity{}, nil, nil, nil, nil
	}
	s := af.Sessions[0]

	// Raw FIT scaling (per FIT profile):
	// total_timer_time: seconds (scale 1000)      -> s.TotalTimerTime / 1000
	// total_distance:  meters (scale 100)         -> s.TotalDistance / 100
	// avg_speed:       m/s (scale 1000)           -> s.AvgSpeed / 1000
	durS   := int(float64(s.TotalTimerTime) / 1000.0)
	distM  := int(float64(s.TotalDistance) / 100.0)
	avgSpd := float64(s.AvgSpeed) / 1000.0

	meta := Activity{
		FitUID:       s.StartTime.UTC().Format(time.RFC3339Nano),
		StartTimeUTC: s.StartTime.UTC(),
		Sport:        s.Sport.String(),
		SubSport:     s.SubSport.String(),
		DurationS:    durS,
		DistanceM:    distM,
		AvgHR:        func() int { if s.AvgHeartRate == 255 { return 0 } else { return int(s.AvgHeartRate) } }(),
		MaxHR:        func() int { if s.MaxHeartRate == 255 { return 0 } else { return int(s.MaxHeartRate) } }(),
		AvgSpeedMPS:  avgSpd,
		Calories:     int(s.TotalCalories),
		AscentM:      float64(s.TotalAscent),
		DescentM:     float64(s.TotalDescent),
	}

	// Extract Garmin-specific training metrics if available
	if s.TotalTrainingEffect != 0 && s.TotalTrainingEffect != 255 {
		val := float64(s.TotalTrainingEffect) / 10.0 // Scale from 0-50 to 0.0-5.0
		meta.AerobicTE = &val
	}
	if s.TotalAnaerobicTrainingEffect != 0 && s.TotalAnaerobicTrainingEffect != 255 {
		val := float64(s.TotalAnaerobicTrainingEffect) / 10.0 // Scale from 0-50 to 0.0-5.0
		meta.AnaerobicTE = &val
	}

	// Records (exported field!)
	var recs []Record
	start := s.StartTime
	for _, rr := range af.Records {
		r := Record{ TOffsetS: int(rr.Timestamp.Sub(start).Seconds()) }

	    // Only keep positions when BOTH are non-zero and inside valid bounds
	    if rr.PositionLat.Semicircles() != 0 && rr.PositionLong.Semicircles() != 0 {
	        lat := float64(rr.PositionLat.Semicircles()) * semicirclesToDeg
	        lon := float64(rr.PositionLong.Semicircles()) * semicirclesToDeg
	        if lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 {
	            r.Lat, r.Lon = &lat, &lon
	        }
	    }

		// Speed (m/s) scale 1000
		if rr.Speed != 0 {
			v := float64(rr.Speed) / 1000.0
			r.SpeedMPS = &v
		}

		// Altitude (meters): scale=5, offset=500  → elev = rr.Altitude/5 - 500
		if rr.Altitude != 0 {
			v := float64(rr.Altitude)/5.0 - 500.0
			r.ElevM = &v
		}

		// HR/Cad/Temp/Power (copy if non-zero)
		if rr.HeartRate != 0 && rr.HeartRate != 255 { v := int(rr.HeartRate); r.HR = &v }
		if rr.Cadence   != 0 { v := int(rr.Cadence);   r.Cad = &v }
		if rr.Temperature != 0 {
			v := float64(int16(rr.Temperature)) // library often exposes as int8/uint8 → cast via int16
			r.TempC = &v
		}
		if rr.Power != 0 { v := int(rr.Power); r.PowerW = &v }

		recs = append(recs, r)
	}

	// Laps
	var laps []Lap
	for i, lp := range af.Laps {
		dur := int(float64(lp.TotalTimerTime) / 1000.0) // s
		dst := int(float64(lp.TotalDistance) / 100.0)   // m
		avg := float64(lp.AvgSpeed) / 1000.0            // m/s
		l := Lap{
			Index:    i,
			StartOff: int(lp.StartTime.Sub(s.StartTime).Seconds()),
			DurS:     dur,
			DistM:    dst,
			AvgHR:    func() int { if lp.AvgHeartRate == 255 { return 0 } else { return int(lp.AvgHeartRate) } }(),
			MaxHR:    func() int { if lp.MaxHeartRate == 255 { return 0 } else { return int(lp.MaxHeartRate) } }(),
			AvgSpd:   avg,
		}
		laps = append(laps, l)
	}

	// Calculate heart rate zones based on records
	zones := calculateHRZones(recs, meta.MaxHR)

	return meta, recs, laps, zones, nil
}

// calculateHRZones calculates time spent in each heart rate zone
// Uses standard 5-zone model based on percentage of max HR
func calculateHRZones(recs []Record, maxHR int) []HRZone {
	if maxHR == 0 {
		return nil
	}

	// Standard HR zone thresholds (percentage of max HR)
	// Zone 1: 50-60% (Recovery)
	// Zone 2: 60-70% (Aerobic Base)
	// Zone 3: 70-80% (Aerobic)
	// Zone 4: 80-90% (Lactate Threshold)
	// Zone 5: 90-100% (Neuromuscular Power)
	zoneThresholds := []float64{
		float64(maxHR) * 0.50, // Zone 1 lower bound
		float64(maxHR) * 0.60, // Zone 2 lower bound
		float64(maxHR) * 0.70, // Zone 3 lower bound
		float64(maxHR) * 0.80, // Zone 4 lower bound
		float64(maxHR) * 0.90, // Zone 5 lower bound
		float64(maxHR) * 1.00, // Zone 5 upper bound
	}

	// Count time in each zone
	zoneTimes := make([]int, 5) // 5 zones

	for i := 0; i < len(recs)-1; i++ {
		curr := recs[i]
		next := recs[i+1]

		if curr.HR == nil {
			continue
		}

		hr := float64(*curr.HR)
		duration := next.TOffsetS - curr.TOffsetS

		// Determine which zone this HR falls into
		var zone int = -1
		for z := 0; z < 5; z++ {
			if hr >= zoneThresholds[z] && hr < zoneThresholds[z+1] {
				zone = z
				break
			}
		}

		if zone >= 0 {
			zoneTimes[zone] += duration
		}
	}

	// Convert to HRZone structs
	var zones []HRZone
	for i, timeSeconds := range zoneTimes {
		if timeSeconds > 0 {
			zones = append(zones, HRZone{
				Zone:        i + 1, // Zone numbers 1-5
				TimeSeconds: timeSeconds,
			})
		}
	}

	return zones
}
