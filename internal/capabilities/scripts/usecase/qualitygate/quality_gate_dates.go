package qualitygate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// sourceDatePreservationChecker catches a factual date mutation at the model
// boundary. It only checks complete numeric dates explicitly supplied in a
// segment's source_text; unstructured years and dates without a year remain
// governed by the normal editorial rules.
type sourceDatePreservationChecker struct{}

func (sourceDatePreservationChecker) Name() string { return "source_date_preservation" }

func (sourceDatePreservationChecker) Check(in qualityGateInput) []string {
	if in.result == nil || len(in.plan.Segments) == 0 {
		return nil
	}
	scenes := in.result.Output.SpecScene.Scenes
	bySegmentID := make(map[string]scriptpkg.SpecScene, len(scenes))
	for _, scene := range scenes {
		if id := strings.TrimSpace(scene.SegmentID); id != "" {
			bySegmentID[id] = scene
		}
	}
	var reasons []string
	for i, segment := range in.plan.Segments {
		expected := explicitCalendarDates(segment.SourceText)
		if len(expected) == 0 {
			continue
		}
		body := ""
		if scene, ok := bySegmentID[strings.TrimSpace(segment.ID)]; ok {
			body = scene.Text
		} else if len(scenes) == len(in.plan.Segments) && i < len(scenes) {
			body = scenes[i].Text
		} else {
			body = in.result.Output.Text
		}
		actual := make(map[calendarDate]struct{})
		for _, date := range explicitCalendarDates(body) {
			actual[date] = struct{}{}
		}
		for _, date := range expected {
			if _, ok := actual[date]; !ok {
				reasons = append(reasons, fmt.Sprintf("segment %q does not preserve source date %s", segment.ID, date))
			}
		}
	}
	return reasons
}

type calendarDate struct {
	day, month, year int
}

func (d calendarDate) String() string {
	return fmt.Sprintf("%02d-%02d-%04d", d.day, d.month, d.year)
}

var (
	calendarDatePattern        = regexp.MustCompile(`(?i)\b(\d{1,2})\s+(gennaio|january|enero|janeiro|febbraio|february|febrero|fevereiro|marzo|march|março|abril|aprile|april|maggio|may|mayo|maio|giugno|june|junio|junho|luglio|july|julio|julho|agosto|august|settembre|september|septiembre|setiembre|setembro|ottobre|october|octubre|outubro|novembre|november|noviembre|novembro|dicembre|december|diciembre|dezembro)\s+(?:(?:del|de|do|di)\s+)?((?:19|20)\d{2})\b`)
	numericCalendarDatePattern = regexp.MustCompile(`\b(\d{1,2})[./-](\d{1,2})[./-]((?:19|20)\d{2})\b`)
	calendarMonthNumbers       = map[string]int{
		"gennaio": 1, "january": 1, "enero": 1, "janeiro": 1,
		"febbraio": 2, "february": 2, "febrero": 2, "fevereiro": 2,
		"marzo": 3, "march": 3, "março": 3,
		"aprile": 4, "april": 4, "abril": 4,
		"maggio": 5, "may": 5, "mayo": 5, "maio": 5,
		"giugno": 6, "june": 6, "junio": 6, "junho": 6,
		"luglio": 7, "july": 7, "julio": 7, "julho": 7,
		"agosto": 8, "august": 8,
		"settembre": 9, "september": 9, "septiembre": 9, "setiembre": 9, "setembro": 9,
		"ottobre": 10, "october": 10, "octubre": 10, "outubro": 10,
		"novembre": 11, "november": 11, "noviembre": 11, "novembro": 11,
		"dicembre": 12, "december": 12, "diciembre": 12, "dezembro": 12,
	}
)

func explicitCalendarDates(text string) []calendarDate {
	var dates []calendarDate
	for _, match := range calendarDatePattern.FindAllStringSubmatch(text, -1) {
		day, dayErr := strconv.Atoi(match[1])
		year, yearErr := strconv.Atoi(match[3])
		month := calendarMonthNumbers[strings.ToLower(match[2])]
		if dayErr == nil && yearErr == nil && validCalendarDate(day, month, year) {
			dates = append(dates, calendarDate{day: day, month: month, year: year})
		}
	}
	for _, match := range numericCalendarDatePattern.FindAllStringSubmatch(text, -1) {
		day, dayErr := strconv.Atoi(match[1])
		month, monthErr := strconv.Atoi(match[2])
		year, yearErr := strconv.Atoi(match[3])
		if dayErr == nil && monthErr == nil && yearErr == nil && validCalendarDate(day, month, year) {
			dates = append(dates, calendarDate{day: day, month: month, year: year})
		}
	}
	return dates
}

func validCalendarDate(day, month, year int) bool {
	if day < 1 || month < 1 || month > 12 {
		return false
	}
	// time.Date normalizes invalid dates; comparing its fields keeps this
	// validation small while handling leap years correctly.
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return date.Year() == year && int(date.Month()) == month && date.Day() == day
}
