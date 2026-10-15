package bi

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MaxChartRows bounds what one chart query returns.
const MaxChartRows = 10000

// Data is what a chart draws: its columns, then one row per group with
// dimension and series cells as text and values as numbers (nil is NULL).
type Data struct {
	Columns []Column `json:"columns"`
	Rows    [][]any  `json:"rows"`
	// Truncated is set when the query had more than MaxChartRows rows.
	Truncated bool `json:"truncated,omitempty"`
	// Complete is false while a shelf is short of fields; the chart can't draw yet.
	Complete bool   `json:"complete"`
	SQL      string `json:"sql,omitempty"`
	// Mode is live or archive; RanAt is when the rows were read.
	Mode  string `json:"mode"`
	RanAt string `json:"ranAt"`
}

// Shape turns raw query cells into chart data: numbers parsed, then the chart's
// sort, Top N (with Others) and value operations applied.
func Shape(c Chart, cols []Column, raw [][]*string) [][]any {
	return ShapeRange(c, cols, raw, nil)
}

// Window is the date range an in_last filter on the dimension asks for; empty
// periods are filled out to its edges.
type Window struct{ From, To time.Time }

// FilterWindow finds an in_last filter (the source's or the chart's) on the
// chart's dimension field.
func FilterWindow(src DataSource, c Chart) *Window {
	if len(c.Dimension) == 0 {
		return nil
	}
	var w *Window
	for _, f := range append(slices.Clone(src.Filters), c.Filters...) {
		if f.Disabled || f.Negate || f.Op != "in_last" || f.Field != c.Dimension[0].Field || f.check() != nil {
			continue
		}
		n, _ := strconv.Atoi(f.Values[0])
		from := Since(now(), n, f.Values[1])
		if w == nil || from.After(w.From) {
			w = &Window{From: from, To: now()}
		}
	}
	return w
}

// ShapeRange is Shape with the dimension's filtered date window, if any.
func ShapeRange(c Chart, cols []Column, raw [][]*string, win *Window) [][]any {
	rows := shape(c, cols, raw, win)
	nameParts(cols, rows)
	return rows
}

var weekdays = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
var months = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// nameParts names extracted weekdays and months ("0" → "Sun", "03" → "Mar")
// once rows are in order.
func nameParts(cols []Column, rows [][]any) {
	for j, col := range cols {
		var names []string
		offset := 0
		switch col.Aggregate {
		case "weekday_x":
			names = weekdays
		case "month_x":
			names, offset = months, 1
		default:
			continue
		}
		for _, r := range rows {
			if s, ok := r[j].(string); ok {
				if i, err := strconv.Atoi(s); err == nil && i-offset >= 0 && i-offset < len(names) {
					r[j] = names[i-offset]
				}
			}
		}
	}
}

func shape(c Chart, cols []Column, raw [][]*string, win *Window) [][]any {
	c.NormalizeSort()
	rows := make([][]any, len(raw))
	for i, r := range raw {
		row := make([]any, len(cols))
		for j, v := range r {
			if v == nil {
				continue
			}
			if cols[j].Type == TypeNumber && cols[j].Role != "dimension" && cols[j].Role != "series" {
				if x, err := strconv.ParseFloat(*v, 64); err == nil {
					row[j] = x
					continue
				}
			}
			row[j] = *v
		}
		rows[i] = row
	}
	switch c.Type {
	case "value":
		return rows
	case "table":
		return shapeTable(c, cols, rows)
	}
	dim := slices.IndexFunc(cols, func(c Column) bool { return c.Role == "dimension" })
	val := valueColumn(c, cols, "value")
	if dim < 0 || val < 0 {
		return rows
	}
	if c.Top == 0 && (c.Sort == nil || c.Sort.By == "dimension") && c.Type != "pie" && c.Type != "donut" {
		rows = fillPeriods(c, cols, rows, dim, win)
	}
	rows = fillSeries(c, cols, rows, dim)
	// Totals per dimension value (over series) decide value order and Top N.
	totals := map[string]float64{}
	var order []string
	for _, r := range rows {
		k := key(r[dim])
		if _, ok := totals[k]; !ok {
			order = append(order, k)
		}
		x, _ := r[val].(float64)
		totals[k] += x
	}
	sortBy := c.Sort
	if sortBy == nil {
		sortBy = defaultSort(c, cols[dim])
	}
	if sortBy.By == "value" {
		slices.SortStableFunc(order, func(a, b string) int { return cmp.Compare(totals[a], totals[b]) })
	} else {
		slices.SortStableFunc(order, func(a, b string) int { return compareLabels(a, b) })
	}
	if sortBy.Desc {
		slices.Reverse(order)
	}
	if c.Top > 0 && len(order) > c.Top {
		kept := order[:c.Top]
		keep := map[string]bool{}
		for _, k := range kept {
			keep[k] = true
		}
		var out [][]any
		others := map[string][]any{} // by series value
		var otherKeys []string
		for _, r := range rows {
			if keep[key(r[dim])] {
				out = append(out, r)
				continue
			}
			if !c.Others {
				continue
			}
			sk := ""
			if s := slices.IndexFunc(cols, func(c Column) bool { return c.Role == "series" }); s >= 0 {
				sk = key(r[s])
			}
			o, ok := others[sk]
			if !ok {
				o = slices.Clone(r)
				o[dim] = "Others"
				for j, col := range cols {
					if col.Role == "value" || col.Role == "secondary" {
						o[j] = 0.0
					}
				}
				others[sk] = o
				otherKeys = append(otherKeys, sk)
			}
			for j, col := range cols {
				if col.Role == "value" || col.Role == "secondary" {
					x, _ := r[j].(float64)
					o[j] = o[j].(float64) + x
				}
			}
		}
		for _, sk := range otherKeys {
			out = append(out, others[sk])
		}
		rows = out
		order = append(slices.Clone(kept), "Others")
	}
	pos := map[string]int{}
	for i, k := range order {
		pos[k] = i
	}
	slices.SortStableFunc(rows, func(a, b []any) int { return cmp.Compare(pos[key(a[dim])], pos[key(b[dim])]) })
	applyOps(c, cols, rows)
	return rows
}

// defaultSort keeps dates and numbers in order, and puts the biggest bars first
// for categories.
func defaultSort(c Chart, dim Column) *ChartSort {
	if dim.Aggregate != "" || dim.Type == TypeDateTime || dim.Type == TypeNumber || strings.Contains(c.Type, "line") || strings.Contains(c.Type, "area") {
		return &ChartSort{By: "dimension"}
	}
	return &ChartSort{By: "value", Desc: true}
}

// applyOps applies value operations along the dimension, per series.
func applyOps(c Chart, cols []Column, rows [][]any) {
	fields := append(slices.Clone(c.Value), c.Secondary...)
	vi := 0
	series := slices.IndexFunc(cols, func(c Column) bool { return c.Role == "series" })
	for j, col := range cols {
		if col.Role != "value" && col.Role != "secondary" {
			continue
		}
		op := fields[vi].Op
		vi++
		if op == "" {
			continue
		}
		prev := map[string]float64{}
		run := map[string]float64{}
		for _, r := range rows {
			sk := ""
			if series >= 0 {
				sk = key(r[series])
			}
			x, ok := r[j].(float64)
			if !ok {
				continue
			}
			switch op {
			case "cumulative":
				run[sk] += x
				r[j] = run[sk]
			case "subtract":
				p, seen := prev[sk]
				prev[sk] = x
				if seen {
					r[j] = x - p
				} else {
					r[j] = nil
				}
			}
		}
	}
}

func key(v any) string {
	if v == nil {
		return "\x00null"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return strconv.FormatFloat(v.(float64), 'g', -1, 64)
}

// compareLabels sorts numbers numerically and text alphabetically; NULL first.
func compareLabels(a, b string) int {
	x, errA := strconv.ParseFloat(a, 64)
	y, errB := strconv.ParseFloat(b, 64)
	if errA == nil && errB == nil {
		return cmp.Compare(x, y)
	}
	return cmp.Compare(strings.ToLower(a), strings.ToLower(b))
}

// FieldType classifies a database type name; "" when the database gave none.
func FieldType(dbType string) string {
	t := strings.ToLower(dbType)
	switch {
	case t == "":
		return ""
	case strings.Contains(t, "interval"):
		return TypeString
	case strings.Contains(t, "date"), strings.Contains(t, "time"):
		return TypeDateTime
	case strings.Contains(t, "int"), strings.Contains(t, "serial"), strings.Contains(t, "numeric"), strings.Contains(t, "decimal"),
		strings.Contains(t, "float"), strings.Contains(t, "double"), strings.Contains(t, "real"), strings.Contains(t, "money"),
		t == "number", t == "dec", t == "fixed", t == "year":
		return TypeNumber
	}
	return TypeString
}

var dateLike = func(s string) bool {
	if len(s) < 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for _, i := range []int{0, 1, 2, 3, 5, 6, 8, 9} {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) == 10 || s[10] == ' ' || s[10] == 'T'
}

// GuessType types a column from sample values, for columns the database
// doesn't type (SQLite expressions, untyped query columns).
func GuessType(samples []string) string {
	if len(samples) == 0 {
		return TypeString
	}
	num, date := true, true
	for _, s := range samples {
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			num = false
		}
		if !dateLike(s) {
			date = false
		}
	}
	switch {
	case num:
		return TypeNumber
	case date:
		return TypeDateTime
	}
	return TypeString
}

// NormalizeDate rewrites a date/time display string for an archive snapshot as
// SQLite's "YYYY-MM-DD HH:MM:SS" (time zone dropped), so strftime reads it.
func NormalizeDate(s string) string {
	if !dateLike(s) {
		return s
	}
	end := 10
	for end < len(s) && end < 26 && strings.ContainsRune("0123456789:. T", rune(s[end])) {
		end++
	}
	return strings.Replace(strings.TrimRight(s[:end], " ."), "T", " ", 1)
}

// valueColumn is the column a sort by value uses: the one the sort names (by
// field or label), else the first column with the role.
func valueColumn(c Chart, cols []Column, role string) int {
	if c.Sort != nil && c.Sort.Field != "" {
		if i := slices.IndexFunc(cols, func(col Column) bool {
			return (col.Role == role || col.Role == "secondary") && (col.Field == c.Sort.Field || col.Label == c.Sort.Field)
		}); i >= 0 {
			return i
		}
	}
	return slices.IndexFunc(cols, func(col Column) bool { return col.Role == role })
}

// shapeTable sorts a table's rows (by its first column, or by a value column:
// the named one or the first aggregated one) and keeps the top n.
func shapeTable(c Chart, cols []Column, rows [][]any) [][]any {
	if c.Sort != nil && len(cols) > 0 {
		idx := 0
		if c.Sort.By == "value" {
			idx = valueColumn(c, cols, "column")
			if c.Sort.Field == "" {
				if i := slices.IndexFunc(cols, func(col Column) bool { return col.Aggregate != "" && col.Type == TypeNumber }); i >= 0 {
					idx = i
				}
			}
		}
		slices.SortStableFunc(rows, func(a, b []any) int {
			r := compareCells(a[idx], b[idx])
			if c.Sort.Desc {
				r = -r
			}
			return r
		})
	}
	if c.Top > 0 && len(rows) > c.Top {
		rows = rows[:c.Top]
	}
	return rows
}

// compareCells orders numbers numerically and text by compareLabels; NULL first.
func compareCells(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	x, okA := a.(float64)
	y, okB := b.(float64)
	if okA && okB {
		return cmp.Compare(x, y)
	}
	return compareLabels(key(a), key(b))
}

// maxFill bounds how many periods filling may add.
const maxFill = 5000

// fillPeriods adds the periods between the first and last that have no rows,
// so a date axis keeps its gaps: sums and counts there are 0, other aggregates
// empty. Only whole periods (year … hour) are filled.
func fillPeriods(c Chart, cols []Column, rows [][]any, dim int, win *Window) [][]any {
	bucket := cols[dim].Aggregate
	if !slices.Contains([]string{"year", "quarter", "month", "week", "day", "hour"}, bucket) || len(rows) == 0 && win == nil {
		return rows
	}
	have := map[string]bool{}
	var lo, hi string
	for _, r := range rows {
		s, ok := r[dim].(string)
		if !ok {
			continue
		}
		have[s] = true
		if lo == "" || s < lo {
			lo = s
		}
		if s > hi {
			hi = s
		}
	}
	start, ok1 := parsePeriod(bucket, lo)
	end, ok2 := parsePeriod(bucket, hi)
	if win != nil {
		// Out to the edges of the filtered window, today included.
		from, _ := parsePeriod(bucket, PeriodLabel(bucket, win.From))
		to, _ := parsePeriod(bucket, PeriodLabel(bucket, win.To))
		if !ok1 || from.Before(start) {
			start, ok1 = from, true
		}
		if !ok2 || to.After(end) {
			end, ok2 = to, true
		}
	}
	if !ok1 || !ok2 {
		return rows
	}
	ser := slices.IndexFunc(cols, func(c Column) bool { return c.Role == "series" })
	if ser >= 0 && len(rows) == 0 {
		return rows // no series to fill
	}
	var series []any
	seen := map[string]bool{}
	for _, r := range rows {
		if ser >= 0 && !seen[key(r[ser])] {
			seen[key(r[ser])] = true
			series = append(series, r[ser])
		}
	}
	if ser < 0 {
		series = []any{nil}
	}
	step := 24 * time.Hour
	if bucket == "hour" {
		step = time.Hour
	}
	added := 0
	for t := start; !t.After(end) && added < maxFill; t = t.Add(step) {
		label := PeriodLabel(bucket, t)
		if have[label] {
			continue
		}
		have[label] = true
		for _, s := range series {
			rows = append(rows, emptyRow(c, cols, dim, ser, label, s))
		}
		added++
	}
	return rows
}

// emptyRow is a group with no source rows: sums and counts are 0, other
// aggregates empty.
func emptyRow(c Chart, cols []Column, dim, ser int, dimValue, serValue any) []any {
	fields := append(slices.Clone(c.Value), c.Secondary...)
	row := make([]any, len(cols))
	row[dim] = dimValue
	if ser >= 0 {
		row[ser] = serValue
	}
	vi := 0
	for j, col := range cols {
		if col.Role != "value" && col.Role != "secondary" {
			continue
		}
		if vi < len(fields) && slices.Contains([]string{"sum", "count", "count_distinct"}, fields[vi].Aggregate) {
			row[j] = 0.0
		}
		vi++
	}
	return row
}

// fillSeries gives every dimension value a row for every series, so a client
// stacking series never meets a hole.
func fillSeries(c Chart, cols []Column, rows [][]any, dim int) [][]any {
	ser := slices.IndexFunc(cols, func(c Column) bool { return c.Role == "series" })
	if ser < 0 {
		return rows
	}
	var dims, series []any
	seenD, seenS, have := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		d, s := key(r[dim]), key(r[ser])
		if !seenD[d] {
			seenD[d] = true
			dims = append(dims, r[dim])
		}
		if !seenS[s] {
			seenS[s] = true
			series = append(series, r[ser])
		}
		have[d+"\x01"+s] = true
	}
	if len(dims)*len(series) > MaxChartRows {
		return rows
	}
	for _, d := range dims {
		for _, s := range series {
			if !have[key(d)+"\x01"+key(s)] {
				rows = append(rows, emptyRow(c, cols, dim, ser, d, s))
			}
		}
	}
	return rows
}

// PeriodLabel labels a time as the chart queries label its period.
func PeriodLabel(bucket string, t time.Time) string {
	switch bucket {
	case "year":
		return t.Format("2006")
	case "quarter":
		return fmt.Sprintf("%d-Q%d", t.Year(), (int(t.Month())+2)/3)
	case "month":
		return t.Format("2006-01")
	case "week":
		return fmt.Sprintf("%d-W%02d", t.Year(), (t.YearDay()-1+7-int(t.Weekday()))/7)
	case "hour":
		return t.Format("2006-01-02 15")
	}
	return t.Format("2006-01-02")
}

// parsePeriod returns the first day (or hour) of a period label.
func parsePeriod(bucket, s string) (time.Time, bool) {
	var t time.Time
	var err error
	switch bucket {
	case "year":
		t, err = time.Parse("2006", s)
	case "quarter":
		var y, q int
		if _, err = fmt.Sscanf(s, "%d-Q%d", &y, &q); err == nil {
			t = time.Date(y, time.Month(q*3-2), 1, 0, 0, 0, 0, time.UTC)
		}
	case "month":
		t, err = time.Parse("2006-01", s)
	case "week":
		var y, w int
		if _, err = fmt.Sscanf(s, "%d-W%d", &y, &w); err == nil {
			t = time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
			for t.Year() == y && PeriodLabel("week", t) != s {
				t = t.AddDate(0, 0, 1)
			}
			if t.Year() != y {
				return t, false
			}
		}
	case "hour":
		t, err = time.Parse("2006-01-02 15", s)
	default:
		t, err = time.Parse("2006-01-02", s)
	}
	return t, err == nil
}
