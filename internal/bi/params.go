package bi

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"chloe/internal/driver"
	"chloe/internal/tz"
)

// Parameter is a named value, global to its workspace, that data source queries,
// filters, chart titles and dashboard text refer to as [$name]. In a query it is
// always a bound value, never pasted into the SQL.
type Parameter struct {
	Meta
	// Type is string, number or datetime.
	Type string `json:"type"`
	// Allowed is "" (any value), list or range.
	Allowed string        `json:"allowed,omitempty"`
	List    []ParamOption `json:"list,omitempty"`
	// Min, Max and Step bound a range parameter (numbers).
	Min  string `json:"min,omitempty"`
	Max  string `json:"max,omitempty"`
	Step string `json:"step,omitempty"`
	// Value is the current value.
	Value string `json:"value"`
}

// ParamOption is one value of a list parameter, with an optional description.
type ParamOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

var (
	paramName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	dollarTag = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)?\$`)
	paramRef  = regexp.MustCompile(`\[\$([A-Za-z_][A-Za-z0-9_]*)\]`)
	wholeRef  = regexp.MustCompile(`^\[\$([A-Za-z_][A-Za-z0-9_]*)\]$`)
)

// Check validates a parameter's settings and current value.
func (p Parameter) Check() error {
	switch {
	case !paramName.MatchString(p.Name):
		return fmt.Errorf("a parameter name is letters, digits and _ (starting with a letter or _), not %q", p.Name)
	case !slices.Contains([]string{TypeString, TypeNumber, TypeDateTime}, p.Type):
		return fmt.Errorf("a parameter's type is string, number or datetime, not %q", p.Type)
	case !slices.Contains([]string{"", "list", "range"}, p.Allowed):
		return fmt.Errorf("allowed values are any, list or range, not %q", p.Allowed)
	case p.Allowed == "list" && len(p.List) == 0:
		return errors.New("a list parameter needs at least one value")
	case p.Allowed == "range" && p.Type != TypeNumber:
		return errors.New("a range parameter is a number")
	}
	if p.Allowed == "range" {
		lo, err1 := strconv.ParseFloat(p.Min, 64)
		hi, err2 := strconv.ParseFloat(p.Max, 64)
		if err1 != nil || err2 != nil || lo > hi {
			return errors.New("a range parameter needs a minimum and a maximum, the minimum not above the maximum")
		}
		if p.Step != "" {
			if s, err := strconv.ParseFloat(p.Step, 64); err != nil || s <= 0 {
				return fmt.Errorf("the step is a positive number, not %q", p.Step)
			}
		}
	}
	for _, o := range p.List {
		if err := p.typed(o.Value); err != nil {
			return err
		}
	}
	return p.Accept(p.Value)
}

func (p Parameter) typed(v string) error {
	switch p.Type {
	case TypeNumber:
		if _, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil {
			return fmt.Errorf("$%s is a number, not %q", p.Name, v)
		}
	case TypeDateTime:
		if _, ok := tz.ParseDateTime(v); !ok {
			return fmt.Errorf("$%s is a date (2026-10-15 or 2026-10-15 09:30), not %q", p.Name, v)
		}
	}
	return nil
}

// Accept checks a value against the parameter's type and allowed values.
func (p Parameter) Accept(v string) error {
	if err := p.typed(v); err != nil {
		return err
	}
	switch p.Allowed {
	case "list":
		if !slices.ContainsFunc(p.List, func(o ParamOption) bool { return o.Value == v }) {
			vals := make([]string, len(p.List))
			for i, o := range p.List {
				vals[i] = strconv.Quote(o.Value)
			}
			return fmt.Errorf("$%s is one of %s, not %q", p.Name, strings.Join(vals, ", "), v)
		}
	case "range":
		x, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		lo, _ := strconv.ParseFloat(p.Min, 64)
		hi, _ := strconv.ParseFloat(p.Max, 64)
		if x < lo || x > hi {
			return fmt.Errorf("$%s is between %s and %s, not %s", p.Name, p.Min, p.Max, v)
		}
	}
	return nil
}

// Param finds a parameter by name (or id).
func (w *Workspace) Param(name string) *Parameter {
	if i := slices.IndexFunc(w.Parameters, func(p Parameter) bool { return p.ID == name || strings.EqualFold(p.Name, name) }); i >= 0 {
		return &w.Parameters[i]
	}
	return nil
}

// PutParameter adds a parameter (no ID yet) or replaces the one with its ID.
func (w *Workspace) PutParameter(p Parameter) (Parameter, error) {
	p.Name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p.Name), "$"))
	if p.Type == "" {
		p.Type = TypeString
	}
	if err := p.Check(); err != nil {
		return p, err
	}
	if err := uniqueName(w.Parameters, &p, "parameter"); err != nil {
		return p, err
	}
	return put(&w.Parameters, p), nil
}

// Values are parameter values by lower-case name.
type Values map[string]string

// Values returns the workspace's parameter values, with overrides (by name)
// checked and applied.
func (w *Workspace) Values(overrides map[string]string) (Values, error) {
	v := Values{}
	for _, p := range w.Parameters {
		v[strings.ToLower(p.Name)] = p.Value
	}
	for name, val := range overrides {
		p := w.Param(strings.TrimPrefix(name, "$"))
		if p == nil {
			return nil, fmt.Errorf("no parameter %q (parameters: %s)", name, strings.Join(w.paramNames(), ", "))
		}
		if err := p.Accept(val); err != nil {
			return nil, err
		}
		v[strings.ToLower(p.Name)] = val
	}
	return v, nil
}

func (w *Workspace) paramNames() []string {
	if len(w.Parameters) == 0 {
		return []string{"none"}
	}
	out := make([]string, len(w.Parameters))
	for i, p := range w.Parameters {
		out[i] = p.Name
	}
	return out
}

// Refs lists the parameter names text refers to, in order, without repeats.
func Refs(s string) []string {
	var out []string
	for _, m := range paramRef.FindAllStringSubmatch(s, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// Expand replaces [$name] in display text (titles, captions); unknown names stay as written.
func (v Values) Expand(s string) string {
	return paramRef.ReplaceAllStringFunc(s, func(m string) string {
		if x, ok := v[strings.ToLower(paramRef.FindStringSubmatch(m)[1])]; ok {
			return x
		}
		return m
	})
}

func (v Values) get(name string) (string, error) {
	x, ok := v[strings.ToLower(name)]
	if !ok {
		return "", fmt.Errorf("no parameter $%s in the workspace", name)
	}
	return x, nil
}

// isRef reports whether a filter value is a whole [$name].
func isRef(s string) bool { return wholeRef.MatchString(strings.TrimSpace(s)) }

// filters returns copies of filters with whole-value references replaced.
func (v Values) filters(fs []Filter) ([]Filter, error) {
	out := make([]Filter, len(fs))
	for i, f := range fs {
		f.Values = slices.Clone(f.Values)
		for j, x := range f.Values {
			if m := wholeRef.FindStringSubmatch(strings.TrimSpace(x)); m != nil {
				val, err := v.get(m[1])
				if err != nil {
					return nil, err
				}
				f.Values[j] = val
			}
		}
		out[i] = f
	}
	return out, nil
}

// Resolve returns copies of a data source and a chart with parameter values
// filled in for one read: filter values that are a [$name], and the values a
// query's [$name] placeholders bind to.
func (w *Workspace) Resolve(src DataSource, c Chart, overrides map[string]string) (DataSource, Chart, error) {
	v, err := w.Values(overrides)
	if err != nil {
		return src, c, err
	}
	// An archive's query ran when its snapshot was taken, with the values then.
	if src.Mode == ModeArchive {
		for name := range overrides {
			if slices.ContainsFunc(Refs(src.SQL), func(r string) bool { return strings.EqualFold(r, strings.TrimPrefix(name, "$")) }) {
				return src, c, fmt.Errorf("$%s is used in archive data source %q's query, which was read with the value it had when the snapshot was taken; change the parameter and refresh the data, or make the source live", strings.TrimPrefix(name, "$"), src.Name)
			}
		}
	}
	if src.Filters, err = v.filters(src.Filters); err != nil {
		return src, c, err
	}
	if c.Filters, err = v.filters(c.Filters); err != nil {
		return src, c, err
	}
	src.params = v
	src.paramTypes = map[string]string{}
	for _, p := range w.Parameters {
		src.paramTypes[strings.ToLower(p.Name)] = p.Type
	}
	return src, c, nil
}

// CheckRefs refuses a data source that names a parameter the workspace doesn't
// have: in its query, its filters' values or its custom fields' expressions.
func (w *Workspace) CheckRefs(src DataSource) error {
	texts := []string{src.SQL}
	for _, f := range src.Filters {
		texts = append(texts, f.Values...)
	}
	for _, cf := range src.CustomFields {
		texts = append(texts, cf.Expression)
	}
	for _, t := range texts {
		for _, name := range Refs(t) {
			if w.Param(name) == nil {
				return fmt.Errorf("no parameter $%s in the workspace (parameters: %s)", name, strings.Join(w.paramNames(), ", "))
			}
		}
	}
	return nil
}

// ParamUsers names the objects that refer to a parameter: "data source X", "chart Y", "dashboard Z".
func (w *Workspace) ParamUsers(name string) []string {
	uses := func(texts ...string) bool {
		for _, t := range texts {
			if slices.ContainsFunc(Refs(t), func(r string) bool { return strings.EqualFold(r, name) }) {
				return true
			}
		}
		return false
	}
	filterTexts := func(fs []Filter) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.Values...)
		}
		return out
	}
	var out []string
	for _, s := range w.DataSources {
		texts := append([]string{s.SQL}, filterTexts(s.Filters)...)
		for _, cf := range s.CustomFields {
			texts = append(texts, cf.Expression)
		}
		if uses(texts...) {
			out = append(out, "data source "+strconv.Quote(s.Name))
		}
	}
	for _, c := range w.Charts {
		if uses(append([]string{c.Props.Title}, filterTexts(c.Filters)...)...) {
			out = append(out, "chart "+strconv.Quote(c.Name))
		}
	}
	for _, d := range w.Dashboards {
		for _, p := range d.Pages {
			if slices.ContainsFunc(p.Items, func(it Item) bool { return uses(it.Text) }) {
				out = append(out, "dashboard "+strconv.Quote(d.Name))
				break
			}
		}
	}
	return out
}

// ResolveSource is Resolve for a data source alone (its fields and preview).
func (w *Workspace) ResolveSource(src DataSource, overrides map[string]string) (DataSource, error) {
	s, _, err := w.Resolve(src, Chart{}, overrides)
	return s, err
}

// bindParams turns the [$name] references of a source query into placeholders,
// numbered from 1, and returns their values. A reference inside a quoted string
// or comment is refused: it can only be a bound value.
func bindParams(d driver.Dialect, sql string, v Values, fields func(string) string) (string, []any, error) {
	var out strings.Builder
	var args []any
	n := len(sql)
	for i := 0; i < n; {
		c := sql[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < n {
				if sql[j] == c {
					if j+1 < n && sql[j+1] == c { // doubled quote
						j += 2
						continue
					}
					break
				}
				// MySQL strings and PostgreSQL E'…' strings take backslash escapes.
				if sql[j] == '\\' && c == '\'' && (d.Name == "mysql" || d.Name == "postgres" && i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e')) {
					j++
				}
				j++
			}
			lit := sql[i:min(j+1, n)]
			if c == '\'' && paramRef.MatchString(lit) {
				return "", nil, fmt.Errorf("write %s without quotes: a parameter is a bound value, so it goes where a value would", paramRef.FindString(lit))
			}
			out.WriteString(lit)
			i = j + 1
		case c == '$' && d.Name == "postgres" && dollarTag.MatchString(sql[i:]):
			// A dollar-quoted string runs to the same tag again.
			tag := dollarTag.FindString(sql[i:])
			end := n
			if j := strings.Index(sql[i+len(tag):], tag); j >= 0 {
				end = i + len(tag) + j + len(tag)
			}
			lit := sql[i:end]
			if paramRef.MatchString(lit) {
				return "", nil, fmt.Errorf("write %s without quotes: a parameter is a bound value, so it goes where a value would", paramRef.FindString(lit))
			}
			out.WriteString(lit)
			i = end
		case c == '-' && i+1 < n && sql[i+1] == '-', c == '#' && d.Name == "mysql":
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = n - i
			}
			out.WriteString(sql[i : i+j])
			i += j
		case c == '/' && i+1 < n && sql[i+1] == '*':
			j := strings.Index(sql[i+2:], "*/")
			end := n
			if j >= 0 {
				end = i + 2 + j + 2
			}
			out.WriteString(sql[i:end])
			i = end
		case c == '[' && i+1 < n && sql[i+1] == '$':
			m := paramRef.FindStringSubmatchIndex(sql[i:])
			if m == nil || m[0] != 0 {
				out.WriteByte(c)
				i++
				continue
			}
			name := sql[i+m[2] : i+m[3]]
			val, err := v.get(name)
			if err != nil {
				return "", nil, err
			}
			var a any = val
			if d.Name == "sqlite" && fields(name) == TypeNumber {
				if x, err := strconv.ParseFloat(val, 64); err == nil {
					a = x
				}
			}
			args = append(args, a)
			out.WriteString(d.Placeholder(len(args)))
			i += m[1]
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), args, nil
}
