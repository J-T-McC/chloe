// Package bi holds BI workspaces (Navicat's BI): data sources that read rows from a
// connection, and charts that aggregate a data source. A workspace is one JSON file
// in Chloe's library; archived rows and snapshots live beside it, encrypted.
// Chart queries are built here from checked field names and bound values.
package bi

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const Format = "chloe-bi"

// FormatVersion 2 halved the dashboard grid's rows (18px rows, 30px steps);
// version 1 files are converted as they're read.
const FormatVersion = 2

// Field types of a data source's columns.
const (
	TypeString   = "string"
	TypeNumber   = "number"
	TypeDateTime = "datetime"
)

// Data source modes.
const (
	ModeLive    = "live"
	ModeArchive = "archive"
)

type Workspace struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	// ID names the folder of the workspace's archived data, so renaming the file keeps it.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Theme is the charts' theme (light or dark), independent of Chloe's appearance.
	Theme       string       `json:"theme,omitempty"`
	CreatedBy   string       `json:"createdBy,omitempty"`
	DataSources []DataSource `json:"dataSources"`
	Charts      []Chart      `json:"charts"`
	Dashboards  []Dashboard  `json:"dashboards"`
	Parameters  []Parameter  `json:"parameters,omitempty"`
}

// Meta is what every workspace object has.
type Meta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// CreatedBy is "mcp:<endpoint>" for objects an agent created; agents may edit only those.
	CreatedBy string `json:"createdBy,omitempty"`
	Created   string `json:"created,omitempty"`
	Modified  string `json:"modified,omitempty"`
	Remarks   string `json:"remarks,omitempty"`
}

// DataSource reads rows from one connection: a table or view, or a query.
type DataSource struct {
	Meta
	ConnID   string `json:"connId"`
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
	// Table is a table or view; SQL is a query. Exactly one is set.
	Table string `json:"table,omitempty"`
	SQL   string `json:"sql,omitempty"`
	// Joins add tables of the same database to a table source.
	Joins []Join `json:"joins,omitempty"`
	// Mode is live (read when used) or archive (read into a snapshot on refresh).
	Mode string `json:"mode"`
	// Timezone (IANA name) is where date/time fields are bucketed and filtered;
	// values without a zone are taken as UTC. "" uses them as stored.
	Timezone   string      `json:"timezone,omitempty"`
	Filters    []Filter    `json:"filters,omitempty"`
	Sorts      []Sort      `json:"sorts,omitempty"`
	Projection *Projection `json:"projection,omitempty"`
	// CustomFields are computed from the source's fields, in order (each may use
	// those before it).
	CustomFields []CustomField `json:"customFields,omitempty"`
	// Fields are the result's columns as last read (before projection).
	Fields []Field `json:"fields,omitempty"`
	// RefreshedAt is when the archive snapshot was taken.
	RefreshedAt string `json:"refreshedAt,omitempty"`
	ArchiveRows int64  `json:"archiveRows,omitempty"`
	// params are the parameter values one read binds (see Workspace.Resolve).
	params     Values
	paramTypes map[string]string
}

type Field struct {
	Name string `json:"name"`
	// Type is string, number or datetime.
	Type string `json:"type"`
	// DBType is the database's type name, when it gave one.
	DBType string `json:"dbType,omitempty"`
}

// Filter is one condition; a data source's or a chart's filters are joined with AND.
type Filter struct {
	Field string `json:"field"`
	// Op is one of the keys of FilterOps.
	Op string `json:"op"`
	// Values holds one value, two for between, or the list for in.
	Values   []string `json:"values,omitempty"`
	Negate   bool     `json:"negate,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

// Sort orders the data source's preview.
type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// Projection keeps or drops fields of the data source.
type Projection struct {
	Exclude bool     `json:"exclude,omitempty"`
	Fields  []string `json:"fields"`
}

// Chart aggregates one data source.
type Chart struct {
	Meta
	DataSource string `json:"dataSource"`
	// Type is one of the keys of ChartTypes.
	Type      string       `json:"type"`
	Dimension []ChartField `json:"dimension,omitempty"`
	Series    []ChartField `json:"series,omitempty"`
	Value     []ChartField `json:"value,omitempty"`
	Secondary []ChartField `json:"secondary,omitempty"`
	// Target is a KPI's target, a gauge's maximum or a scatter's Y axis.
	Target []ChartField `json:"target,omitempty"`
	// Filters filter the source rows for this chart only (Navicat's Data Filter).
	Filters []Filter   `json:"filters,omitempty"`
	Sort    *ChartSort `json:"sort,omitempty"`
	// Top keeps the first n dimension values; with Others the rest add up into one.
	Top    int   `json:"top,omitempty"`
	Others bool  `json:"others,omitempty"`
	Props  Props `json:"props"`
}

// ChartField is a field placed on a shelf.
type ChartField struct {
	Field string `json:"field"`
	// Aggregate is a value's aggregate (sum, avg…) or a date dimension's bucket
	// (month, quarter_x…); "" is the plain value.
	Aggregate string `json:"aggregate,omitempty"`
	Alias     string `json:"alias,omitempty"`
	// Op is cumulative or subtract (difference from the previous value).
	Op string `json:"op,omitempty"`
}

// ChartSort orders a chart by its dimension or by its first value.
type ChartSort struct {
	By string `json:"by"` // dimension or value
	// Field picks the value (by field name or label) when a chart has several; a
	// table sorts by its first aggregated column otherwise.
	Field string `json:"field,omitempty"`
	Desc  bool   `json:"desc,omitempty"`
}

// Props are display settings; anything left empty comes from the theme.
type Props struct {
	Title     string `json:"title,omitempty"`
	HideTitle bool   `json:"hideTitle,omitempty"`
	// Legend is auto, top, bottom, right or none.
	Legend string `json:"legend,omitempty"`
	Labels bool   `json:"labels,omitempty"`
	// LabelPercent shows percentages on pie and donut labels.
	LabelPercent bool `json:"labelPercent,omitempty"`
	// Stack100 draws stacked bars and areas as shares of 100 %.
	Stack100 bool         `json:"stack100,omitempty"`
	Smooth   bool         `json:"smooth,omitempty"`
	Number   NumberFormat `json:"number,omitzero"`
	// DateFormat uses Navicat's specifiers (YYYY, MMM, DD…); "" picks one per bucket.
	DateFormat string `json:"dateFormat,omitempty"`
	// Palette names a theme palette; Colors overrides it in series order.
	Palette string   `json:"palette,omitempty"`
	Colors  []string `json:"colors,omitempty"`
	XTitle  string   `json:"xTitle,omitempty"`
	// LabelCase is how category and series values are shown: "" makes enum-like
	// values readable (ended_early → Ended Early), raw shows them as stored.
	LabelCase string `json:"labelCase,omitempty"`
	YTitle    string `json:"yTitle,omitempty"`
	// Goal is a KPI's target and Min/Max a gauge's range, when no Target field
	// gives them.
	Goal *float64 `json:"goal,omitempty"`
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	// LowerIsBetter colours a Trend's or KPI's change: a fall is good.
	LowerIsBetter bool `json:"lowerIsBetter,omitempty"`
	// SameDays has a Trend whose latest period is still going (this week on a
	// Thursday) compare it with the same days of the period before (Sunday to
	// Thursday last week) instead of that whole period.
	SameDays bool `json:"sameDays,omitempty"`
	// Control is how a Control chart picks values: list (several), combo (one)
	// or range (numbers and dates); "" picks by the field's type.
	Control string `json:"control,omitempty"`
}

type NumberFormat struct {
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
	// Unit shortens numbers: auto, K, M or B.
	Unit     string `json:"unit,omitempty"`
	Decimals *int   `json:"decimals,omitempty"`
	// NoSeparator drops the thousands separator.
	NoSeparator bool `json:"noSeparator,omitempty"`
	// Percent shows values as percentages (0.2 → 20%).
	Percent bool `json:"percent,omitempty"`
}

// New returns an empty workspace.
func New(name string) Workspace {
	return Workspace{Format: Format, Version: FormatVersion, ID: uuid.NewString(), Name: name, DataSources: []DataSource{}, Charts: []Chart{}, Dashboards: []Dashboard{}}
}

func Parse(data []byte) (Workspace, error) {
	var w Workspace
	if err := json.Unmarshal(data, &w); err != nil {
		return w, fmt.Errorf("not a Chloe BI workspace: %w", err)
	}
	if w.Format != Format {
		return w, errors.New("not a Chloe BI workspace file")
	}
	if w.ID == "" {
		w.ID = uuid.NewString()
	}
	if w.DataSources == nil {
		w.DataSources = []DataSource{}
	}
	if w.Charts == nil {
		w.Charts = []Chart{}
	}
	if w.Dashboards == nil {
		w.Dashboards = []Dashboard{}
	}
	if w.Version < 2 {
		// Twice the rows at half the height: every tile keeps its pixels.
		for i := range w.Dashboards {
			for j := range w.Dashboards[i].Pages {
				for k := range w.Dashboards[i].Pages[j].Items {
					it := &w.Dashboards[i].Pages[j].Items[k]
					it.Y, it.H = it.Y*2, it.H*2
				}
			}
		}
	}
	w.Version = FormatVersion
	return w, nil
}

func (w Workspace) Marshal() ([]byte, error) { return json.MarshalIndent(w, "", "  ") }

func (m *Meta) meta() *Meta { return m }

// object is a pointer to a workspace object: data source, chart, dashboard or parameter.
type object[T any] interface {
	*T
	meta() *Meta
}

// find returns the object with this id, or nil.
func find[T any, P object[T]](list []T, id string) *T {
	if i := slices.IndexFunc(list, func(v T) bool { return P(&v).meta().ID == id }); i >= 0 {
		return &list[i]
	}
	return nil
}

// uniqueName refuses a name another object of the same kind already has, so
// agents and users can refer to objects by kind and name. A chart may share
// its data source's name.
func uniqueName[T any, P object[T]](list []T, v P, kind string) error {
	m := v.meta()
	for i := range list {
		if o := P(&list[i]).meta(); o.ID != m.ID && strings.EqualFold(o.Name, m.Name) {
			return fmt.Errorf("the workspace already has a %s named %q", kind, m.Name)
		}
	}
	return nil
}

// put adds v (no ID yet) or replaces the object with its ID, keeping when and by
// whom the object was created, and returns it as stored.
func put[T any, P object[T]](list *[]T, v T) T {
	m := P(&v).meta()
	m.Modified = stamp()
	if old := find[T, P](*list, m.ID); m.ID != "" && old != nil {
		om := P(old).meta()
		m.Created, m.CreatedBy = om.Created, om.CreatedBy
		*old = v
		return v
	}
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	m.Created = m.Modified
	*list = append(*list, v)
	return v
}

// Lookup finds an object by id, else by name (case-insensitive).
func Lookup[T any, P object[T]](list []T, ref string) *T {
	if v := find[T, P](list, ref); v != nil {
		return v
	}
	if i := slices.IndexFunc(list, func(v T) bool { return strings.EqualFold(P(&v).meta().Name, ref) }); i >= 0 {
		return &list[i]
	}
	return nil
}

func (w *Workspace) Source(id string) *DataSource { return find(w.DataSources, id) }

func (w *Workspace) Chart(id string) *Chart { return find(w.Charts, id) }

// ChartsUsing lists the charts built on a data source.
func (w *Workspace) ChartsUsing(sourceID string) []Chart {
	var out []Chart
	for _, c := range w.Charts {
		if c.DataSource == sourceID {
			out = append(out, c)
		}
	}
	return out
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339) }

// PutSource adds a data source (no ID yet) or replaces the one with its ID, and
// returns it as stored.
func (w *Workspace) PutSource(d DataSource) (DataSource, error) {
	d.Name = strings.TrimSpace(d.Name)
	if d.Mode == "" {
		d.Mode = ModeLive
	}
	if err := d.Validate(); err != nil {
		return d, err
	}
	if err := uniqueName(w.DataSources, &d, "data source"); err != nil {
		return d, err
	}
	return put(&w.DataSources, d), nil
}

// PutChart adds a chart (no ID yet) or replaces the one with its ID.
func (w *Workspace) PutChart(c Chart) (Chart, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return c, errors.New("a chart needs a name")
	}
	if w.Source(c.DataSource) == nil {
		return c, fmt.Errorf("no data source %q in the workspace", c.DataSource)
	}
	c.NormalizeSort()
	if _, ok := ChartTypes[c.Type]; !ok {
		return c, fmt.Errorf("unknown chart type %q (types: %s)", c.Type, strings.Join(TypeNames(), ", "))
	}
	if err := uniqueName(w.Charts, &c, "chart"); err != nil {
		return c, err
	}
	return put(&w.Charts, c), nil
}

// Delete removes an object. Charts built on a deleted data source stay and show
// that their source is gone; a deleted chart's tiles leave its dashboards.
func (w *Workspace) Delete(id string) error {
	n := len(w.DataSources) + len(w.Charts) + len(w.Dashboards) + len(w.Parameters)
	w.DataSources = slices.DeleteFunc(w.DataSources, func(d DataSource) bool { return d.ID == id })
	w.Charts = slices.DeleteFunc(w.Charts, func(c Chart) bool { return c.ID == id })
	w.Dashboards = slices.DeleteFunc(w.Dashboards, func(d Dashboard) bool { return d.ID == id })
	w.Parameters = slices.DeleteFunc(w.Parameters, func(p Parameter) bool { return p.ID == id })
	if len(w.DataSources)+len(w.Charts)+len(w.Dashboards)+len(w.Parameters) == n {
		return fmt.Errorf("no object %q in the workspace", id)
	}
	for i := range w.Dashboards {
		for j := range w.Dashboards[i].Pages {
			p := &w.Dashboards[i].Pages[j]
			p.Items = slices.DeleteFunc(p.Items, func(it Item) bool { return it.Kind == ItemChart && it.Chart == id })
		}
	}
	return nil
}

// Validate checks a data source's own settings (not its fields, which need the database).
func (d DataSource) Validate() error {
	switch {
	case d.Name == "":
		return errors.New("a data source needs a name")
	case d.ConnID == "":
		return errors.New("a data source needs a connection")
	case (d.Table == "") == (strings.TrimSpace(d.SQL) == ""):
		return errors.New("a data source reads either a table or view, or a query")
	case d.Mode != ModeLive && d.Mode != ModeArchive:
		return fmt.Errorf("mode is live or archive, not %q", d.Mode)
	}
	if err := CheckTimezone(d.Timezone); err != nil {
		return err
	}
	for _, f := range d.Filters {
		if err := f.check(); err != nil {
			return err
		}
	}
	if err := d.checkJoins(); err != nil {
		return err
	}
	if err := d.checkCustom(); err != nil {
		return err
	}
	// Once the fields are known, filters are checked against their types.
	for _, f := range d.Filters {
		if fd, ok := fieldIn(d.allFields(), f.Field); ok {
			if err := checkFieldFilter(f, fd); err != nil {
				return err
			}
		}
	}
	return nil
}

// allFields are the fields a source's rows have: its columns and custom fields.
func (d DataSource) allFields() []Field {
	custom, _ := d.customFields()
	return append(slices.Clone(d.Fields), custom...)
}

// Visible lists the fields charts may use: the source's fields after
// projection, then its custom fields.
func (d DataSource) Visible() []Field {
	custom, _ := d.customFields()
	return append(slices.Clone(d.baseFields()), custom...)
}

// Field finds a visible field by name.
func (d DataSource) Field(name string) (Field, bool) {
	i := slices.IndexFunc(d.Visible(), func(f Field) bool { return f.Name == name })
	if i < 0 {
		return Field{}, false
	}
	return d.Visible()[i], true
}
