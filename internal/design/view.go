package design

import (
	"fmt"
	"strings"
)

// View is a view or PostgreSQL materialized view as the view designer edits it.
type View struct {
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
	Name     string `json:"name"`
	// OrigName is the name when loaded; empty for a new view.
	OrigName     string `json:"origName,omitempty"`
	Materialized bool   `json:"materialized,omitempty"`
	// Definition is the SELECT statement.
	Definition string `json:"definition"`
	// CheckOption is "", CASCADED or LOCAL (not for materialized views).
	CheckOption string `json:"checkOption,omitempty"`

	// MySQL
	Algorithm string `json:"algorithm,omitempty"` // UNDEFINED, MERGE, TEMPTABLE
	Definer   string `json:"definer,omitempty"`   // user@host; empty means the current user
	Security  string `json:"security,omitempty"`  // DEFINER or INVOKER

	// PostgreSQL
	SecurityBarrier bool   `json:"securityBarrier,omitempty"`
	SecurityInvoker bool   `json:"securityInvoker,omitempty"`
	Tablespace      string `json:"tablespace,omitempty"`
	// NoData creates a materialized view without populating it (WITH NO DATA).
	NoData  bool   `json:"noData,omitempty"`
	Comment string `json:"comment,omitempty"`
}

func (v View) definition() string {
	return strings.TrimRight(strings.TrimSpace(v.Definition), "; \t\n")
}

// ViewSQL returns the statements that create v (old nil) or change old into v, and
// warnings about side effects the user should confirm.
func ViewSQL(dialect string, old *View, v View) (stmts, warnings []string, err error) {
	if strings.TrimSpace(v.Name) == "" {
		return nil, nil, fmt.Errorf("enter a view name")
	}
	if v.definition() == "" {
		return nil, nil, fmt.Errorf("enter the view's SELECT statement")
	}
	if dialect == "postgres" {
		return pgViewSQL(old, v)
	}
	if v.Materialized {
		return nil, nil, fmt.Errorf("MySQL has no materialized views")
	}
	return myViewSQL(old, v)
}

func myViewSQL(old *View, v View) ([]string, []string, error) {
	qual := func(name string) string {
		if v.Database != "" {
			return myq(v.Database) + "." + myq(name)
		}
		return myq(name)
	}
	create := func(orReplace bool) string {
		var b strings.Builder
		b.WriteString("CREATE ")
		if orReplace {
			b.WriteString("OR REPLACE ")
		}
		if v.Algorithm != "" {
			b.WriteString("ALGORITHM = " + strings.ToUpper(v.Algorithm) + " ")
		}
		if d := strings.TrimSpace(v.Definer); d != "" {
			b.WriteString("DEFINER = " + myDefiner(d) + " ")
		}
		if v.Security != "" {
			b.WriteString("SQL SECURITY " + strings.ToUpper(v.Security) + " ")
		}
		b.WriteString("VIEW " + qual(v.Name) + " AS\n" + v.definition())
		if v.CheckOption != "" {
			b.WriteString("\nWITH " + strings.ToUpper(v.CheckOption) + " CHECK OPTION")
		}
		return b.String()
	}
	if old == nil {
		return []string{create(false)}, nil, nil
	}
	var out []string
	if old.Name != v.Name {
		out = append(out, "RENAME TABLE "+qual(old.Name)+" TO "+qual(v.Name))
	}
	cur := *old
	cur.Name, cur.Database = v.Name, v.Database
	if cur.definition() != v.definition() || !strings.EqualFold(cur.Algorithm, v.Algorithm) || cur.Definer != v.Definer ||
		!strings.EqualFold(cur.Security, v.Security) || !strings.EqualFold(cur.CheckOption, v.CheckOption) {
		out = append(out, create(true))
	}
	return out, nil, nil
}

// myDefiner quotes user@host; CURRENT_USER passes through.
func myDefiner(d string) string {
	if strings.EqualFold(d, "CURRENT_USER") || strings.HasPrefix(d, "`") {
		return d
	}
	user, host, ok := strings.Cut(d, "@")
	if !ok {
		return myq(d)
	}
	return myq(strings.Trim(user, "'`")) + "@" + myq(strings.Trim(host, "'`"))
}

func pgViewSQL(old *View, v View) ([]string, []string, error) {
	qual := func(name string) string {
		if v.Schema != "" {
			return pgq(v.Schema) + "." + pgq(name)
		}
		return pgq(name)
	}
	kind := "VIEW"
	if v.Materialized {
		kind = "MATERIALIZED VIEW"
	}
	var warnings []string
	comment := func(name string) string {
		c := "NULL"
		if v.Comment != "" {
			c = pgStr(v.Comment)
		}
		return "COMMENT ON " + kind + " " + qual(name) + " IS " + c
	}
	opts := func() string {
		var o []string
		if v.CheckOption != "" {
			o = append(o, "check_option = "+strings.ToLower(v.CheckOption))
		}
		if v.SecurityBarrier {
			o = append(o, "security_barrier = true")
		}
		if v.SecurityInvoker {
			o = append(o, "security_invoker = true")
		}
		if len(o) == 0 {
			return ""
		}
		return " WITH (" + strings.Join(o, ", ") + ")"
	}
	create := func(orReplace bool) string {
		if v.Materialized {
			s := "CREATE MATERIALIZED VIEW " + qual(v.Name)
			if v.Tablespace != "" {
				s += " TABLESPACE " + pgq(v.Tablespace)
			}
			s += " AS\n" + v.definition()
			if v.NoData {
				return s + "\nWITH NO DATA"
			}
			return s + "\nWITH DATA"
		}
		s := "CREATE "
		if orReplace {
			s += "OR REPLACE "
		}
		return s + "VIEW " + qual(v.Name) + opts() + " AS\n" + v.definition()
	}
	if old == nil {
		out := []string{create(false)}
		if v.Comment != "" {
			out = append(out, comment(v.Name))
		}
		return out, nil, nil
	}

	if old.Materialized != v.Materialized {
		oldKind := "VIEW"
		if old.Materialized {
			oldKind = "MATERIALIZED VIEW"
		}
		warnings = append(warnings, fmt.Sprintf("%s is dropped and re-created as a %s; objects that depend on it block the change.", old.Name, strings.ToLower(kind)))
		out := []string{"DROP " + oldKind + " " + qual(old.Name), create(false)}
		if v.Comment != "" {
			out = append(out, comment(v.Name))
		}
		return out, warnings, nil
	}

	var out []string
	if old.Name != v.Name {
		out = append(out, "ALTER "+kind+" "+qual(old.Name)+" RENAME TO "+pgq(v.Name))
	}
	defChanged := old.definition() != v.definition()
	if v.Materialized {
		if defChanged || old.NoData != v.NoData {
			warnings = append(warnings, fmt.Sprintf("Changing a materialized view's query drops and re-creates %s: its indexes and privileges are lost, and objects that depend on it block the change.", v.Name))
			out = append(out, "DROP MATERIALIZED VIEW "+qual(v.Name), create(false))
			if v.Comment != "" {
				out = append(out, comment(v.Name))
			}
			return out, warnings, nil
		}
		if old.Tablespace != v.Tablespace && v.Tablespace != "" {
			out = append(out, "ALTER MATERIALIZED VIEW "+qual(v.Name)+" SET TABLESPACE "+pgq(v.Tablespace))
		}
	} else {
		if defChanged {
			// States every option, so the result doesn't depend on what was there.
			out = append(out, create(true))
		} else {
			var set, reset []string
			opt := func(name string, was, is bool) {
				switch {
				case is && !was:
					set = append(set, name+" = true")
				case was && !is:
					reset = append(reset, name)
				}
			}
			opt("security_barrier", old.SecurityBarrier, v.SecurityBarrier)
			opt("security_invoker", old.SecurityInvoker, v.SecurityInvoker)
			if !strings.EqualFold(old.CheckOption, v.CheckOption) {
				if v.CheckOption == "" {
					reset = append(reset, "check_option")
				} else {
					set = append(set, "check_option = "+strings.ToLower(v.CheckOption))
				}
			}
			if len(set) > 0 {
				out = append(out, "ALTER VIEW "+qual(v.Name)+" SET ("+strings.Join(set, ", ")+")")
			}
			if len(reset) > 0 {
				out = append(out, "ALTER VIEW "+qual(v.Name)+" RESET ("+strings.Join(reset, ", ")+")")
			}
		}
	}
	if old.Comment != v.Comment {
		out = append(out, comment(v.Name))
	}
	return out, warnings, nil
}
