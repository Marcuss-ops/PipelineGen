// Pure parsers for the /proc, sysfs and nvidia-smi sources consumed by
// Provider.readSnapshot / readGPU. Every parser fails soft: malformed input
// yields zero values, never an error, so a quirky kernel or a truncated read
// degrades the observation to nil fields instead of failing a run.
package procmetrics

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// parseProcStat extracts utime and stime (fields 14/15, USER_HZ ticks) from
// /proc/<pid>/stat. The comm field may contain spaces and parens, so the
// token stream is split after the LAST ')'.
func parseProcStat(content string) (utime, stime uint64) {
	idx := strings.LastIndex(content, ")")
	if idx < 0 || idx+1 >= len(content) {
		return 0, 0
	}
	fields := strings.Fields(content[idx+1:])
	// fields[0]=state(3); utime=14, stime=15 → offsets 11, 12.
	if len(fields) < 13 {
		return 0, 0
	}
	return parseUint(fields[11]), parseUint(fields[12])
}

// parseProcStatus extracts VmRSS and VmHWM (kB) from /proc/<pid>/status.
func parseProcStatus(content string) (rssKB, hwmKB int64) {
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(line, "VmRSS:"):
			rssKB = parseKBValue(line)
		case strings.HasPrefix(line, "VmHWM:"):
			hwmKB = parseKBValue(line)
		}
	}
	return rssKB, hwmKB
}

func parseKBValue(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	return int64(parseUint(fields[1]))
}

// parseProcIO extracts read_bytes and write_bytes from /proc/<pid>/io.
func parseProcIO(content string) (readBytes, writeBytes int64) {
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(line, "read_bytes:"):
			readBytes = parseKVInt(line)
		case strings.HasPrefix(line, "write_bytes:"):
			writeBytes = parseKVInt(line)
		}
	}
	return readBytes, writeBytes
}

func parseKVInt(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	return int64(parseUint(fields[1]))
}

// parseStatCpu extracts total, idle and iowait tick counts from the first
// /proc/stat line ("cpu  user nice system idle iowait irq softirq steal ...").
func parseStatCpu(line string) (total, idle, iowait uint64) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, 0
	}
	var sum uint64
	for _, f := range fields[1:] {
		sum += parseUint(f)
	}
	idle = parseUint(fields[4])
	if len(fields) > 5 {
		iowait = parseUint(fields[5])
	}
	return sum, idle, iowait
}

// diskRow is one /proc/diskstats line.
type diskRow struct {
	name         string
	sectorsRead  uint64
	sectorsWrite uint64
	msDoingIO    uint64
	weightedMS   uint64
}

// parseDiskstats parses /proc/diskstats. Fields (1-based): 1 major, 2 minor,
// 3 name, 4 reads, 5 reads merged, 6 sectors read, 7 ms reading, 8 writes,
// 9 writes merged, 10 sectors written, 11 ms writing, 12 ios in progress,
// 13 ms doing io, 14 weighted ms doing io.
func parseDiskstats(content string) []diskRow {
	var rows []diskRow
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}
		rows = append(rows, diskRow{
			name:         fields[2],
			sectorsRead:  parseUint(fields[6]),
			sectorsWrite: parseUint(fields[10]),
			msDoingIO:    parseUint(fields[12]),
			weightedMS:   parseUint(fields[13]),
		})
	}
	return rows
}

// aggregateDisks sums the whole-disk rows (partitions are excluded so a disk
// is never double-counted) and returns (msDoingIO, weightedMS, diskCount).
func aggregateDisks(rows []diskRow) (doingMS, weightedMS uint64, count int) {
	whole := wholeDisks(rows)
	for _, r := range whole {
		doingMS += r.msDoingIO
		weightedMS += r.weightedMS
	}
	return doingMS, weightedMS, len(whole)
}

// wholeDisks keeps devices that are not a partition of another listed
// device. Partition suffixes are either all digits (sda1 → sda) or
// p<digits> (nvme0n1p1 → nvme0n1, mmcblk0p1 → mmcblk0); the whole-disk
// sibling must itself be listed.
func wholeDisks(rows []diskRow) []diskRow {
	names := make(map[string]bool, len(rows))
	for _, r := range rows {
		names[r.name] = true
	}
	out := rows[:0]
	for _, r := range rows {
		if isPartition(r.name, names) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func isPartition(name string, names map[string]bool) bool {
	for disk := range names {
		if disk == name || !strings.HasPrefix(name, disk) {
			continue
		}
		rest := name[len(disk):]
		if rest == "" {
			continue
		}
		if rest[0] >= '0' && rest[0] <= '9' {
			return true
		}
		if rest[0] == 'p' && len(rest) > 1 && allDigits(rest[1:]) {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseNetDev sums rx_bytes/tx_bytes across interfaces (loopback excluded).
func parseNetDev(content string) (rx, tx uint64) {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, ":") {
			continue
		}
		head, rest, _ := strings.Cut(trimmed, ":")
		if strings.TrimSpace(head) == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rx += parseUint(fields[0])
		tx += parseUint(fields[8])
	}
	return rx, tx
}

// parseVMStat extracts pswpin/pswpout (pages) from /proc/vmstat.
func parseVMStat(content string) (swapInPages, swapOutPages uint64) {
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(line, "pswpin "):
			swapInPages = parseUint(strings.TrimSpace(strings.TrimPrefix(line, "pswpin ")))
		case strings.HasPrefix(line, "pswpout "):
			swapOutPages = parseUint(strings.TrimSpace(strings.TrimPrefix(line, "pswpout ")))
		}
	}
	return swapInPages, swapOutPages
}

// readThermalZones returns the highest temperature (°C) across all thermal
// zones. /sys/class/thermal/thermal_zone*/temp values are in milli°C.
func readThermalZones(sysRoot string) float64 {
	zones := filepath.Join(sysRoot, "class", "thermal")
	entries, err := os.ReadDir(zones)
	if err != nil {
		return 0
	}
	var maxTemp float64
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "thermal_zone") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(zones, entry.Name(), "temp"))
		if err != nil {
			continue
		}
		milli := parseUint(strings.TrimSpace(string(content)))
		if temp := float64(milli) / 1000; temp > maxTemp {
			maxTemp = temp
		}
	}
	return maxTemp
}

// readThrottleCounts reports whether any CPU's x86 thermal-throttle counter
// (core or package) is non-zero. Non-x86 hosts lack the sysfs files and
// report false.
func readThrottleCounts(sysRoot string) bool {
	cpus := filepath.Join(sysRoot, "devices", "system", "cpu")
	entries, err := os.ReadDir(cpus)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "cpu") {
			continue
		}
		throttleDir := filepath.Join(cpus, entry.Name(), "thermal_throttle")
		for _, counter := range []string{"core_throttle_count", "package_throttle_count"} {
			content, err := os.ReadFile(filepath.Join(throttleDir, counter))
			if err != nil {
				continue
			}
			if parseUint(strings.TrimSpace(string(content))) > 0 {
				return true
			}
		}
	}
	return false
}

// parseGPUOutput parses nvidia-smi CSV output lines
// ("62, 1234, 40, 55, 63" per GPU) and aggregates across GPUs: max
// utilization/encoder/decoder/temperature, summed memory.used (MiB → bytes).
func parseGPUOutput(content string) (gpuSample, bool) {
	var (
		sample    gpuSample
		parsedAny bool
	)
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 5 {
			continue
		}
		util := parseFloat(fields[0])
		usedMiB := parseFloat(fields[1])
		enc := parseFloat(fields[2])
		dec := parseFloat(fields[3])
		temp := parseFloat(fields[4])
		if util > sample.utilPct {
			sample.utilPct = util
		}
		sample.usedBytes += int64(usedMiB * 1024 * 1024)
		if enc > sample.encoderPct {
			sample.encoderPct = enc
		}
		if dec > sample.decoderPct {
			sample.decoderPct = dec
		}
		if temp > sample.tempC {
			sample.tempC = temp
		}
		parsedAny = true
	}
	return sample, parsedAny
}

func readFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

func readFirstLine(path string) string {
	content := readFile(path)
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		return content[:idx]
	}
	return content
}

func parseUint(s string) uint64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

func f64p(v float64) *float64 { return &v }
func i64p(v int64) *int64     { return &v }
