package jobs

import "time"

const claimAgingHourMillis int64 = 3_600_000

// initialClaimAging projects the current score and the next full-hour
// threshold for a newly inserted job. Keeping both values aligned with the
// canonical created_at timestamp also handles imported/future-dated rows
// without waiting for the first claim to repair their projection.
func initialClaimAging(priority int, createdAt, now time.Time) (effectivePriority int, nextAgeMillis int64) {
	createdMillis := createdAt.UTC().UnixMilli()
	ageHours := (now.UTC().UnixMilli() - createdMillis) / claimAgingHourMillis
	return priority + int(ageHours), createdMillis + (ageHours+1)*claimAgingHourMillis
}
