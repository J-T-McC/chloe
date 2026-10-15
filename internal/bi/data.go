package bi

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
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
	if c.Type == "table" || c.Type == "value" {
		return rows
	}
	dim := slices.IndexFunc(cols, func(c Column) bool { return c.Role == "dimension" })
	val := slices.IndexFunc(cols, func(c Column) bool { return c.Role == "value" })
	if dim < 0 || val < 0 {
		return rows
	}
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
