package bi

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Shelf limits of a chart type: Min..Max fields, Max 0 meaning the shelf isn't offered.
type Shelf struct {
	Min int `json:"min"`
	Max int `json:"max"`
	// Label is the shelf's name in the designer when it isn't the default.
	Label string `json:"label,omitempty"`
}

type ChartType struct {
	Name      string `json:"name"`
	Group     string `json:"group"`
	Dimension Shelf  `json:"dimension"`
	Series    Shelf  `json:"series"`
	Value     Shelf  `json:"value"`
	Secondary Shelf  `json:"secondary"`
	Target    Shelf  `json:"target"`
	// Raw lets value fields go without an aggregate (table columns).
	Raw bool `json:"raw,omitempty"`
	// DateDimension requires the dimension to be a date/time field (Trend).
	DateDimension bool `json:"dateDimension,omitempty"`
	// NoFill keeps rows as read: no empty periods or series are added (points of
	// a scatter, slices of a treemap).
	NoFill bool `json:"noFill,omitempty"`
}

const many = 8

// ChartTypes are the chart types and their shelves (Navicat's, from its sample workspace).
var ChartTypes = map[string]ChartType{
	"bar":              {Name: "Bar", Group: "Bar", Dimension: Shelf{1, 1, "X-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "Y-Axis"}},
	"stacked_bar":      {Name: "Stacked Bar", Group: "Bar", Dimension: Shelf{1, 1, "X-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "Y-Axis"}},
	"hbar":             {Name: "Horizontal Bar", Group: "Bar", Dimension: Shelf{1, 1, "Y-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "X-Axis"}},
	"stacked_hbar":     {Name: "Horizontal Stacked Bar", Group: "Bar", Dimension: Shelf{1, 1, "Y-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "X-Axis"}},
	"line":             {Name: "Line", Group: "Line / Area", Dimension: Shelf{1, 1, "X-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "Y-Axis"}},
	"area":             {Name: "Area", Group: "Line / Area", Dimension: Shelf{1, 1, "X-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "Y-Axis"}},
	"stacked_area":     {Name: "Stacked Area", Group: "Line / Area", Dimension: Shelf{1, 1, "X-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "Y-Axis"}},
	"bar_line":         {Name: "Bar and Line", Group: "Bar and Line", Dimension: Shelf{1, 1, "X-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "Bar Values"}, Secondary: Shelf{1, many, "Line Values"}},
	"stacked_bar_line": {Name: "Stacked Bar and Line", Group: "Bar and Line", Dimension: Shelf{1, 1, "X-Axis"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, many, "Bar Values"}, Secondary: Shelf{1, many, "Line Values"}},
	"pie":              {Name: "Pie", Group: "Pie / Donut", Dimension: Shelf{1, 1, "Category"}, Value: Shelf{1, 1, ""}},
	"donut":            {Name: "Donut", Group: "Pie / Donut", Dimension: Shelf{1, 1, "Category"}, Value: Shelf{1, 1, ""}},
	"value":            {Name: "Value", Group: "Value", Value: Shelf{1, 1, ""}},
	"table":            {Name: "Table", Group: "Table", Value: Shelf{1, 32, "Columns"}, Raw: true},
	"waterfall":        {Name: "Waterfall", Group: "Bar", Dimension: Shelf{1, 1, "X-Axis"}, Value: Shelf{1, 1, "Y-Axis"}},
	"tornado":          {Name: "Tornado", Group: "Bar", Dimension: Shelf{1, 1, "Y-Axis"}, Series: Shelf{1, 1, "Series (two values)"}, Value: Shelf{1, 1, "X-Axis"}},
	"treemap":          {Name: "Treemap", Group: "Pie / Donut", Dimension: Shelf{1, 1, "Category"}, Value: Shelf{1, 1, "Size"}, NoFill: true},
	"scatter":          {Name: "Scatter", Group: "Scatter", Dimension: Shelf{1, 1, "Point"}, Series: Shelf{0, 1, ""}, Value: Shelf{1, 1, "X-Axis"}, Target: Shelf{1, 1, "Y-Axis"}, Secondary: Shelf{0, 1, "Size"}, NoFill: true},
	"heatmap":          {Name: "Heatmap", Group: "Heatmap", Dimension: Shelf{1, 1, "X-Axis"}, Series: Shelf{1, 1, "Y-Axis"}, Value: Shelf{1, 1, "Intensity"}},
	"trend":            {Name: "Trend", Group: "Value", Dimension: Shelf{1, 1, "Date"}, Value: Shelf{1, 1, ""}, DateDimension: true},
	"kpi":              {Name: "KPI", Group: "Value", Value: Shelf{1, 1, ""}, Target: Shelf{0, 1, ""}},
	"gauge":            {Name: "Gauge", Group: "Value", Value: Shelf{1, 1, ""}, Target: Shelf{0, 1, "Maximum"}},
	"pivot":            {Name: "Pivot Table", Group: "Table", Dimension: Shelf{1, 1, "Rows"}, Series: Shelf{0, 1, "Columns"}, Value: Shelf{1, many, "Values"}},
}

// Single reports whether a type draws one aggregated row (no dimension).
func (t ChartType) Single() bool { return t.Dimension.Max == 0 && !t.Raw }

// TypeNames lists the chart type keys, sorted.
func TypeNames() []string {
	out := make([]string, 0, len(ChartTypes))
	for k := range ChartTypes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Aggregates of value fields by field type.
var ValueAggregates = map[string][]string{
	TypeNumber:   {"sum", "avg", "count", "count_distinct", "min", "max", "median"},
	TypeDateTime: {"count", "count_distinct", "min", "max"},
	TypeString:   {"count", "count_distinct", "min", "max"},
}

// DateBuckets group a date/time dimension: whole periods, and the _x forms that
// extract one part (all Marches together).
var DateBuckets = []string{"year", "quarter", "month", "week", "day", "hour", "minute", "second",
	"quarter_x", "month_x", "week_x", "day_x", "weekday_x", "hour_x", "minute_x", "second_x"}

// DefaultAggregate is what a field gets when dropped on a shelf.
func DefaultAggregate(shelf string, f Field, raw bool) string {
	if shelf == "dimension" || shelf == "series" {
		if f.Type == TypeDateTime {
			return "month"
		}
		return ""
	}
	if raw {
		return ""
	}
	if f.Type == TypeNumber {
		return "sum"
	}
	return "count"
}

// AllowedAggregates lists what a field on a shelf may use.
func AllowedAggregates(shelf string, f Field, raw bool, dialect string) []string {
	if shelf == "dimension" || shelf == "series" {
		if f.Type == TypeDateTime {
			return append([]string{""}, DateBuckets...)
		}
		return []string{""}
	}
	out := slices.Clone(ValueAggregates[f.Type])
	if dialect != "postgres" {
		out = slices.DeleteFunc(out, func(a string) bool { return a == "median" })
	}
	if raw {
		// A table column groups by its plain value or, for dates, by a bucket.
		lead := []string{""}
		if f.Type == TypeDateTime {
			lead = append(lead, DateBuckets...)
		}
		out = append(lead, out...)
	}
	return out
}

// IsBucket reports whether an aggregate is a date bucket (a grouping).
func IsBucket(agg string) bool { return slices.Contains(DateBuckets, agg) }

// describe names an aggregate or bucket for messages; "" is the plain value.
func describe(agg string) string {
	if agg == "" {
		return `"" (the plain value)`
	}
	return fmt.Sprintf("%q", agg)
}

var aggregateNames = map[string]string{
	"sum": "Sum", "avg": "Average", "count": "Count", "count_distinct": "Count (Distinct)", "min": "Min", "max": "Max", "median": "Median",
	"year": "Year", "quarter": "Quarter", "month": "Month", "week": "Week", "day": "Day", "hour": "Hour", "minute": "Minute", "second": "Second",
	"quarter_x": "Quarter (Extract)", "month_x": "Month (Extract)", "week_x": "Week (Extract)", "day_x": "Day (Extract)",
	"weekday_x": "Weekday (Extract)", "hour_x": "Hour (Extract)", "minute_x": "Minute (Extract)", "second_x": "Second (Extract)",
}

// Label is how a chart field is titled in legends, tooltips and table headers:
// its alias as typed, or the field name made readable, e.g. "Sum of Total" or
// "Placed At (Month)".
func (f ChartField) Label() string {
	if f.Alias != "" {
		return f.Alias
	}
	name := Humanize(f.Field)
	switch {
	case f.Aggregate == "":
		return name
	case slices.Contains(DateBuckets, f.Aggregate):
		return fmt.Sprintf("%s (%s)", name, aggregateNames[f.Aggregate])
	}
	return aggregateNames[f.Aggregate] + " of " + name
}

var acronyms = map[string]string{"id": "ID", "ids": "IDs", "url": "URL", "uri": "URI", "sku": "SKU", "api": "API", "ip": "IP", "uuid": "UUID", "kpi": "KPI", "vip": "VIP", "utc": "UTC"}

// Humanize turns a column name into a label: snake_case, kebab-case and
// camelCase become words, each capitalized ("customer_id" -> "Customer ID").
// Words with capitals of their own keep them.
func Humanize(name string) string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.':
			flush()
			continue
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1])):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	if len(words) == 0 {
		return name
	}
	for i, w := range words {
		if a, ok := acronyms[strings.ToLower(w)]; ok && w == strings.ToLower(w) {
			words[i] = a
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// NormalizeSort turns a sort by a value's name ({by: "Finished"}) into
// {by: "value", field: "Finished"}.
func (c *Chart) NormalizeSort() {
	if c.Sort == nil || c.Sort.By == "dimension" || c.Sort.By == "value" {
		return
	}
	for _, cf := range append(slices.Clone(c.Value), c.Secondary...) {
		if strings.EqualFold(cf.Field, c.Sort.By) || strings.EqualFold(cf.Label(), c.Sort.By) || cf.Alias != "" && strings.EqualFold(cf.Alias, c.Sort.By) {
			s := *c.Sort
			s.By, s.Field = "value", cf.Field
			if cf.Alias != "" {
				s.Field = cf.Alias
			}
			c.Sort = &s
			return
		}
	}
}

// shelves returns a chart's shelves by name, in query order.
func (c Chart) shelves() []struct {
	name   string
	fields []ChartField
	limits Shelf
} {
	t := ChartTypes[c.Type]
	return []struct {
		name   string
		fields []ChartField
		limits Shelf
	}{{"dimension", c.Dimension, t.Dimension}, {"series", c.Series, t.Series}, {"value", c.Value, t.Value}, {"secondary", c.Secondary, t.Secondary}, {"target", c.Target, t.Target}}
}

// Check validates a chart against its data source's fields. Incomplete charts
// (a shelf below its minimum) aren't errors in the designer, so complete reports
// them separately.
func (c Chart) Check(src DataSource, dialect string) (complete bool, err error) {
	t, ok := ChartTypes[c.Type]
	if !ok {
		return false, fmt.Errorf("unknown chart type %q (types: %s)", c.Type, strings.Join(TypeNames(), ", "))
	}
	complete = true
	for _, sh := range c.shelves() {
		if len(sh.fields) > sh.limits.Max {
			if sh.limits.Max == 0 {
				return false, fmt.Errorf("a %s chart has no %s shelf", t.Name, sh.name)
			}
			return false, fmt.Errorf("a %s chart takes at most %d %s field(s)", t.Name, sh.limits.Max, sh.name)
		}
		if len(sh.fields) < sh.limits.Min {
			complete = false
		}
		for _, cf := range sh.fields {
			f, ok := src.Field(cf.Field)
			if !ok {
				return false, fmt.Errorf("data source %q has no field %q (fields: %s)", src.Name, cf.Field, strings.Join(fieldNames(src.Visible()), ", "))
			}
			allowed := AllowedAggregates(sh.name, f, t.Raw, dialect)
			if !slices.Contains(allowed, cf.Aggregate) {
				var names []string
				for _, a := range allowed {
					names = append(names, describe(a))
				}
				what := "aggregate"
				if sh.name == "dimension" || sh.name == "series" || IsBucket(cf.Aggregate) {
					what = "bucket"
				}
				return false, fmt.Errorf("%s (%s) on the %s shelf can't use %s %s (allowed: %s)", cf.Field, f.Type, sh.name, what, describe(cf.Aggregate), strings.Join(names, ", "))
			}
			if cf.Op != "" && cf.Op != "cumulative" && cf.Op != "subtract" {
				return false, fmt.Errorf("value operation is cumulative or subtract, not %q", cf.Op)
			}
		}
	}
	if len(c.Series) > 0 && len(c.Value) > 1 {
		return false, fmt.Errorf("a chart split by series takes one value field")
	}
	if t.DateDimension && len(c.Dimension) > 0 {
		if f, _ := src.Field(c.Dimension[0].Field); f.Type != TypeDateTime || c.Dimension[0].Aggregate == "" || strings.HasSuffix(c.Dimension[0].Aggregate, "_x") {
			return false, fmt.Errorf("a %s chart's dimension is a date/time field grouped by period (year, quarter, month, week, day or hour)", t.Name)
		}
	}
	c.NormalizeSort()
	if c.Sort != nil && c.Sort.By != "dimension" && c.Sort.By != "value" {
		var labels []string
		for _, cf := range append(slices.Clone(c.Value), c.Secondary...) {
			labels = append(labels, fmt.Sprintf("%q", cf.Label()))
		}
		return false, fmt.Errorf("sort by is dimension, value, or one of the chart's values (%s), not %q", strings.Join(labels, ", "), c.Sort.By)
	}
	if c.Sort != nil && c.Sort.Field != "" {
		ok := false
		for _, cf := range append(slices.Clone(c.Value), c.Secondary...) {
			ok = ok || cf.Field == c.Sort.Field || cf.Label() == c.Sort.Field || cf.Alias == c.Sort.Field
		}
		if !ok {
			return false, fmt.Errorf("sort field %q isn't one of the chart's values", c.Sort.Field)
		}
	}
	if c.Top < 0 {
		return false, fmt.Errorf("top can't be negative")
	}
	for _, f := range c.Filters {
		if err := f.check(); err != nil {
			return false, err
		}
		if _, ok := fieldIn(src.Fields, f.Field); !ok {
			return false, fmt.Errorf("data source %q has no field %q", src.Name, f.Field)
		}
	}
	return complete, nil
}

func fieldNames(fs []Field) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

func fieldIn(fs []Field, name string) (Field, bool) {
	i := slices.IndexFunc(fs, func(f Field) bool { return f.Name == name })
	if i < 0 {
		return Field{}, false
	}
	return fs[i], true
}
