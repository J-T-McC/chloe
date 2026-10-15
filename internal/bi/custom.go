package bi

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"chloe/internal/driver"
)

// Custom field kinds (Navicat's: Type-Changed, Concatenated, Mapped, Custom-Sorted
// and Calculated).
const (
	CustomTypeChanged = "type_changed"
	CustomConcat      = "concatenated"
	CustomMapped      = "mapped"
	CustomSorted      = "sorted"
	CustomCalculated  = "calculated"
)

// CustomField is a field a data source computes from its own fields.
type CustomField struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Field is the field it's made from (type_changed, mapped, sorted).
	Field string `json:"field,omitempty"`
	// To is a type_changed field's type: string, number or datetime.
	To string `json:"to,omitempty"`
	// Fields and Separator make a concatenated field.
	Fields    []string `json:"fields,omitempty"`
	Separator string   `json:"separator,omitempty"`
	// Rules map values (mapped); CaseSensitive compares them exactly. Unmapped
	// values stay as they are ("" or keep), become NULL (null) or Other (other).
	Rules         []MapRule `json:"rules,omitempty"`
	CaseSensitive bool      `json:"caseSensitive,omitempty"`
	Unmapped      string    `json:"unmapped,omitempty"`
	Other         string    `json:"other,omitempty"`
	// Order is a sorted field's order of values; others follow, in their own order.
	Order []string `json:"order,omitempty"`
	// Expression is a calculated field's formula (see expr.go).
	Expression string `json:"expression,omitempty"`
	// Type is the field's type as last worked out (string, number, datetime);
	// set when the source is saved, for the designer.
	Type string `json:"type,omitempty"`
}

// TypeCustom records each custom field's type (see CustomField.Type).
func (d *DataSource) TypeCustom() {
	fs, _ := d.customFields()
	for i := range d.CustomFields {
		if i < len(fs) {
			d.CustomFields[i].Type = fs[i].Type
		}
	}
}

// MapRule maps one value to another.
type MapRule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// baseFields are the source's fields before custom fields, after projection.
func (d DataSource) baseFields() []Field {
	if d.Projection == nil || len(d.Projection.Fields) == 0 {
		return d.Fields
	}
	var out []Field
	for _, f := range d.Fields {
		if slices.Contains(d.Projection.Fields, f.Name) != d.Projection.Exclude {
			out = append(out, f)
		}
	}
	return out
}

// customFields types the custom fields: each may use the fields before it.
func (d DataSource) customFields() ([]Field, error) {
	known := slices.Clone(d.Fields)
	var out []Field
	for _, cf := range d.CustomFields {
		t, err := cf.typeOf(func(name string) (Field, bool) { return fieldIn(known, name) })
		if err != nil {
			return out, fmt.Errorf("custom field %q: %w", cf.Name, err)
		}
		f := Field{Name: cf.Name, Type: t, DBType: "custom"}
		known = append(known, f)
		out = append(out, f)
	}
	return out, nil
}

func (cf CustomField) typeOf(field func(string) (Field, bool)) (string, error) {
	need := func(name string) (Field, error) {
		f, ok := field(name)
		if !ok {
			return f, fmt.Errorf("no field %q", name)
		}
		return f, nil
	}
	switch cf.Kind {
	case CustomTypeChanged:
		if _, err := need(cf.Field); err != nil {
			return "", err
		}
		if !slices.Contains([]string{TypeString, TypeNumber, TypeDateTime}, cf.To) {
			return "", fmt.Errorf("changes to string, number or datetime, not %q", cf.To)
		}
		return cf.To, nil
	case CustomConcat:
		if len(cf.Fields) < 2 {
			return "", errors.New("joins two or more fields")
		}
		for _, n := range cf.Fields {
			if _, err := need(n); err != nil {
				return "", err
			}
		}
		return TypeString, nil
	case CustomMapped:
		f, err := need(cf.Field)
		if err != nil {
			return "", err
		}
		if len(cf.Rules) == 0 {
			return "", errors.New("needs at least one rule")
		}
		if !slices.Contains([]string{"", "keep", "null", "other"}, cf.Unmapped) {
			return "", fmt.Errorf("unmapped values keep, become null or other, not %q", cf.Unmapped)
		}
		_ = f
		return TypeString, nil
	case CustomSorted:
		f, err := need(cf.Field)
		if err != nil {
			return "", err
		}
		if len(cf.Order) == 0 {
			return "", errors.New("needs the order of its values")
		}
		return f.Type, nil
	case CustomCalculated:
		n, err := parseExpr(cf.Expression)
		if err != nil {
			return "", err
		}
		c := &exprCompiler{d: SQLite, fields: field, bind: func(any) string { return "?" }}
		_, t, err := c.compile(n)
		return t, err
	}
	return "", fmt.Errorf("unknown kind %q (type_changed, concatenated, mapped, sorted, calculated)", cf.Kind)
}

// checkCustom validates a data source's custom fields against its fields.
func (d DataSource) checkCustom() error {
	seen := map[string]bool{}
	for _, f := range d.Fields {
		seen[strings.ToLower(f.Name)] = true
	}
	for _, cf := range d.CustomFields {
		name := strings.TrimSpace(cf.Name)
		switch {
		case name == "":
			return errors.New("a custom field needs a name")
		case seen[strings.ToLower(name)]:
			return fmt.Errorf("the data source already has a field named %q", name)
		}
		seen[strings.ToLower(name)] = true
	}
	if len(d.Fields) == 0 {
		return nil // typed once the source's fields are read
	}
	_, err := d.customFields()
	return err
}

// withCustom wraps a FROM target so its rows carry the custom fields too:
// (SELECT s.*, <expr> AS "name"… FROM <from> AS s). Values are bound: for
// PostgreSQL numbered after the inner target's, for ? dialects placed before them
// (the select list comes first in the text).
func withCustom(d driver.Dialect, from Source, src DataSource) (Source, error) {
	if len(src.CustomFields) == 0 {
		return from, nil
	}
	args := slices.Clone(from.Args)
	known := slices.Clone(src.Fields)
	field := func(name string) (Field, bool) { return fieldIn(known, name) }
	inner := from.SQL
	for _, cf := range src.CustomFields {
		var layer []any
		bind := func(v any) string {
			layer = append(layer, v)
			if d.Name == "postgres" {
				return d.Placeholder(len(args) + len(layer))
			}
			return d.Placeholder(len(layer))
		}
		expr, t, err := customSQL(d, cf, field, src.params, bind)
		if err != nil {
			return Source{}, fmt.Errorf("custom field %q: %w", cf.Name, err)
		}
		known = append(known, Field{Name: cf.Name, Type: t})
		// A later custom field may use this one, so each gets its own layer. Its
		// values come first in the text, so before the inner ones for ? dialects.
		inner = "(SELECT s.*, " + expr + " AS " + d.QuoteIdent(cf.Name) + " FROM " + inner + " AS s)"
		if d.Name == "postgres" {
			args = append(args, layer...)
		} else {
			args = append(layer, args...)
		}
	}
	return Source{SQL: inner, Args: args}, nil
}

// customSQL is one custom field's expression.
func customSQL(d driver.Dialect, cf CustomField, field func(string) (Field, bool), params Values, bind func(any) string) (string, string, error) {
	col := func(name string) (string, Field, error) {
		f, ok := field(name)
		if !ok {
			return "", f, fmt.Errorf("no field %q", name)
		}
		return d.QuoteIdent(f.Name), f, nil
	}
	text := func(x string) string {
		if d.Name == "mysql" {
			return "CAST(" + x + " AS CHAR)"
		}
		return "CAST(" + x + " AS TEXT)"
	}
	switch cf.Kind {
	case CustomTypeChanged:
		x, _, err := col(cf.Field)
		if err != nil {
			return "", "", err
		}
		switch cf.To {
		case TypeString:
			return text(x), TypeString, nil
		case TypeNumber:
			switch d.Name {
			case "postgres":
				return "CAST(NULLIF(CAST(" + x + " AS TEXT), '') AS numeric)", TypeNumber, nil
			case "mysql":
				return "CAST(" + x + " AS DECIMAL(65, 10))", TypeNumber, nil
			}
			return "CAST(" + x + " AS REAL)", TypeNumber, nil
		case TypeDateTime:
			switch d.Name {
			case "postgres":
				return "CAST(NULLIF(CAST(" + x + " AS TEXT), '') AS timestamp)", TypeDateTime, nil
			case "mysql":
				return "CAST(" + x + " AS DATETIME)", TypeDateTime, nil
			}
			return "datetime(" + x + ")", TypeDateTime, nil
		}
		return "", "", fmt.Errorf("unknown type %q", cf.To)
	case CustomConcat:
		parts := make([]string, len(cf.Fields))
		for i, n := range cf.Fields {
			x, _, err := col(n)
			if err != nil {
				return "", "", err
			}
			parts[i] = "COALESCE(" + text(x) + ", '')"
		}
		sep := bind(cf.Separator)
		if d.Name == "mysql" {
			return "CONCAT_WS(" + sep + ", " + strings.Join(parts, ", ") + ")", TypeString, nil
		}
		// The separator is bound once per gap.
		out := parts[0]
		for i, p := range parts[1:] {
			s := sep
			if i > 0 {
				s = bind(cf.Separator)
			}
			out += " || " + s + " || " + p
		}
		return "(" + out + ")", TypeString, nil
	case CustomMapped:
		x, _, err := col(cf.Field)
		if err != nil {
			return "", "", err
		}
		subject := text(x)
		if !cf.CaseSensitive {
			subject = "LOWER(" + subject + ")"
		}
		var b strings.Builder
		b.WriteString("(CASE")
		for _, r := range cf.Rules {
			from := r.From
			if !cf.CaseSensitive {
				from = strings.ToLower(from)
			}
			b.WriteString(" WHEN " + subject + " = " + bind(from) + " THEN " + bind(r.To))
		}
		switch cf.Unmapped {
		case "null":
			b.WriteString(" ELSE NULL")
		case "other":
			b.WriteString(" ELSE " + bind(cf.Other))
		default:
			b.WriteString(" ELSE " + text(x))
		}
		b.WriteString(" END)")
		return b.String(), TypeString, nil
	case CustomSorted:
		x, f, err := col(cf.Field)
		return x, f.Type, err
	case CustomCalculated:
		n, err := parseExpr(cf.Expression)
		if err != nil {
			return "", "", err
		}
		c := &exprCompiler{d: d, fields: field, params: params, bind: bind}
		return c.compile(n)
	}
	return "", "", fmt.Errorf("unknown kind %q", cf.Kind)
}

// sortOrder is the hand-made order of a sorted custom field, if name is one.
func (d DataSource) sortOrder(name string) []string {
	for _, cf := range d.CustomFields {
		if cf.Name == name && cf.Kind == CustomSorted {
			return cf.Order
		}
	}
	return nil
}
