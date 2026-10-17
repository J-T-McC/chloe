package design

import (
	"fmt"
	"slices"
	"strings"

	"chloe/internal/sqlq"
	"chloe/internal/sqlsplit"
)

// MySQL generates MySQL/MariaDB DDL.
type MySQL struct{}

func myq(s string) string { return sqlq.Backtick(s) }

func myStr(s string) string { return sqlq.MySQLString(s) }

func (MySQL) table(t Table) string {
	if t.Database != "" {
		return myq(t.Database) + "." + myq(t.Name)
	}
	return myq(t.Name)
}

func (MySQL) column(c Column) string {
	var b strings.Builder
	b.WriteString(myq(c.Name) + " " + TypeSQL(c))
	if c.Unsigned {
		b.WriteString(" UNSIGNED")
	}
	if c.Zerofill {
		b.WriteString(" ZEROFILL")
	}
	if c.Charset != "" {
		b.WriteString(" CHARACTER SET " + c.Charset)
	}
	if c.Collation != "" {
		b.WriteString(" COLLATE " + c.Collation)
	}
	if c.GeneratedExpr != "" {
		kind := "VIRTUAL"
		if c.GeneratedStored {
			kind = "STORED"
		}
		fmt.Fprintf(&b, " GENERATED ALWAYS AS (%s) %s", c.GeneratedExpr, kind)
	}
	if c.Nullable {
		b.WriteString(" NULL")
	} else {
		b.WriteString(" NOT NULL")
	}
	if c.Default != nil && c.GeneratedExpr == "" {
		b.WriteString(" DEFAULT " + *c.Default)
	}
	if c.OnUpdateNow {
		// The ON UPDATE precision must match the column's fractional seconds.
		b.WriteString(" ON UPDATE CURRENT_TIMESTAMP")
		if c.Length != "" {
			b.WriteString("(" + c.Length + ")")
		}
	}
	if c.AutoIncrement {
		b.WriteString(" AUTO_INCREMENT")
	}
	if c.Comment != "" {
		b.WriteString(" COMMENT " + myStr(c.Comment))
	}
	return b.String()
}

func (MySQL) indexCols(cols []IndexColumn) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		p := myq(c.Name)
		if c.Expr != "" {
			p = "(" + c.Expr + ")"
		}
		if c.Length != "" {
			p += "(" + c.Length + ")"
		}
		if c.Desc {
			p += " DESC"
		}
		parts[i] = p
	}
	return strings.Join(parts, ", ")
}

func quoteList(cols []string, q func(string) string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = q(c)
	}
	return strings.Join(parts, ", ")
}

func (g MySQL) indexDef(ix Index) string {
	kind := "INDEX"
	switch ix.Kind {
	case "unique":
		kind = "UNIQUE INDEX"
	case "fulltext":
		kind = "FULLTEXT INDEX"
	case "spatial":
		kind = "SPATIAL INDEX"
	}
	s := fmt.Sprintf("%s %s (%s)", kind, myq(ix.Name), g.indexCols(ix.Columns))
	if ix.Method != "" && ix.Kind != "fulltext" && ix.Kind != "spatial" {
		s += " USING " + ix.Method
	}
	if ix.Comment != "" {
		s += " COMMENT " + myStr(ix.Comment)
	}
	if ix.Invisible {
		s += " INVISIBLE"
	}
	return s
}

func (MySQL) fkDef(fk ForeignKey) string {
	ref := myq(fk.RefTable)
	if fk.RefDatabase != "" {
		ref = myq(fk.RefDatabase) + "." + ref
	}
	s := fmt.Sprintf("CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)", myq(fk.Name), quoteList(fk.Columns, myq), ref, quoteList(fk.RefColumns, myq))
	if fk.OnDelete != "" {
		s += " ON DELETE " + fk.OnDelete
	}
	if fk.OnUpdate != "" {
		s += " ON UPDATE " + fk.OnUpdate
	}
	return s
}

func (MySQL) checkDef(c Check) string {
	s := fmt.Sprintf("CONSTRAINT %s CHECK (%s)", myq(c.Name), c.Expr)
	if c.NotEnforced {
		s += " NOT ENFORCED"
	}
	return s
}

func (MySQL) tableOptions(o Options, comment string, includeEmpty bool) []string {
	var opts []string
	if o.Engine != "" {
		opts = append(opts, "ENGINE="+o.Engine)
	}
	if o.Charset != "" {
		opts = append(opts, "DEFAULT CHARSET="+o.Charset)
	}
	if o.Collation != "" {
		opts = append(opts, "COLLATE="+o.Collation)
	}
	if o.AutoIncrement != "" {
		opts = append(opts, "AUTO_INCREMENT="+o.AutoIncrement)
	}
	if o.RowFormat != "" {
		opts = append(opts, "ROW_FORMAT="+o.RowFormat)
	}
	if comment != "" || includeEmpty {
		opts = append(opts, "COMMENT="+myStr(comment))
	}
	return opts
}

func (g MySQL) triggerDDL(t Table, tr Trigger) string {
	// MySQL triggers fire on exactly one event.
	return fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s FOR EACH ROW %s", myq(tr.Name), tr.Timing, tr.Events[0], g.table(t), strings.TrimSpace(tr.Body))
}

func (g MySQL) Create(t Table) ([]string, error) {
	return checked(t, nil, sqlsplit.MySQL, func() ([]string, error) { return g.create(t) })
}

func (g MySQL) create(t Table) ([]string, error) {
	if err := Validate(t); err != nil {
		return nil, err
	}
	if err := g.validate(t); err != nil {
		return nil, err
	}
	var lines []string
	for _, c := range t.Columns {
		lines = append(lines, "  "+g.column(c))
	}
	if t.PrimaryKey != nil && len(t.PrimaryKey.Columns) > 0 {
		lines = append(lines, "  PRIMARY KEY ("+quoteList(t.PrimaryKey.Columns, myq)+")")
	}
	for _, ix := range t.Indexes {
		lines = append(lines, "  "+g.indexDef(ix))
	}
	for _, fk := range t.ForeignKeys {
		lines = append(lines, "  "+g.fkDef(fk))
	}
	for _, c := range t.Checks {
		lines = append(lines, "  "+g.checkDef(c))
	}
	stmt := fmt.Sprintf("CREATE TABLE %s (\n%s\n)", g.table(t), strings.Join(lines, ",\n"))
	if opts := g.tableOptions(t.Options, t.Comment, false); len(opts) > 0 {
		stmt += " " + strings.Join(opts, " ")
	}
	out := []string{stmt}
	for _, tr := range t.Triggers {
		out = append(out, g.triggerDDL(t, tr))
	}
	return out, nil
}

func (MySQL) validate(t Table) error {
	for _, tr := range t.Triggers {
		if len(tr.Events) != 1 {
			return fmt.Errorf("trigger %q: MySQL triggers fire on exactly one event", tr.Name)
		}
		if strings.TrimSpace(tr.Body) == "" {
			return fmt.Errorf("trigger %q has no body", tr.Name)
		}
	}
	autoInc := 0
	for _, c := range t.Columns {
		if c.AutoIncrement {
			autoInc++
		}
	}
	if autoInc > 1 {
		return fmt.Errorf("a table can have only one AUTO_INCREMENT column")
	}
	return nil
}

// Alter builds one ALTER TABLE with every clause (MySQL applies them atomically per
// statement), followed by trigger changes, which need their own statements.
func (g MySQL) Alter(old, nw Table) ([]string, error) {
	return checked(nw, &old, sqlsplit.MySQL, func() ([]string, error) { return g.alter(old, nw) })
}

func (g MySQL) alter(old, nw Table) ([]string, error) {
	if err := Validate(nw); err != nil {
		return nil, err
	}
	if err := g.validate(nw); err != nil {
		return nil, err
	}
	d := diffTables(old, nw)
	var out []string
	if nw.Name != old.Name {
		out = append(out, fmt.Sprintf("RENAME TABLE %s TO %s", g.table(old), g.table(nw)))
	}
	// One ALTER TABLE: foreign keys, checks and indexes that changed go before the
	// columns change and come back after them, then the table options.
	clauses := g.dropParts(d)
	cols, err := g.columnClauses(d)
	if err != nil {
		return nil, err
	}
	clauses = append(clauses, cols...)
	clauses = append(clauses, g.addParts(d)...)
	if opts := tableOptionChanges(old, nw); len(opts) > 0 {
		clauses = append(clauses, strings.Join(opts, " "))
	}
	if len(clauses) > 0 {
		out = append(out, "ALTER TABLE "+g.table(nw)+"\n  "+strings.Join(clauses, ",\n  "))
	}

	// Triggers: drop changed/removed, create new/changed.
	dropTr, addTr := d.triggers()
	for _, tr := range dropTr {
		db := ""
		if nw.Database != "" {
			db = myq(nw.Database) + "."
		}
		out = append(out, "DROP TRIGGER "+db+myq(tr.Name))
	}
	for _, tr := range addTr {
		out = append(out, g.triggerDDL(nw, tr))
	}
	return out, nil
}

func (g MySQL) dropParts(d tableDiff) []string {
	var clauses []string
	fks, _ := d.foreignKeys()
	for _, fk := range fks {
		clauses = append(clauses, "DROP FOREIGN KEY "+myq(fk.Name))
	}
	cks, _ := d.checks(true)
	for _, ck := range cks {
		clauses = append(clauses, "DROP CHECK "+myq(ck.Name))
	}
	ixs, _ := d.indexes()
	for _, ix := range ixs {
		clauses = append(clauses, "DROP INDEX "+myq(ix.Name))
	}
	if oldPK, newPK := d.pk(); !equalStrings(oldPK, newPK) && len(oldPK) > 0 {
		clauses = append(clauses, "DROP PRIMARY KEY")
	}
	return clauses
}

// columnClauses drops, modifies or renames (with position) and adds columns.
// It follows the column order as MySQL applies the clauses: cur starts as the
// surviving columns in their old order, and each new column either already sits
// at its target index (no position clause) or is moved or added there with
// FIRST/AFTER.
func (g MySQL) columnClauses(d tableDiff) ([]string, error) {
	var clauses []string
	for _, c := range d.droppedColumns() {
		clauses = append(clauses, "DROP COLUMN "+myq(c.Name))
	}
	cur := d.survivors() // original names; added columns are "+name"
	for i, c := range d.nw.Columns {
		pos := " FIRST"
		if i > 0 {
			pos = " AFTER " + myq(d.nw.Columns[i-1].Name)
		}
		if c.OrigName == "" {
			clauses = append(clauses, "ADD COLUMN "+g.column(c)+pos)
			cur = slices.Insert(cur, min(i, len(cur)), "+"+c.Name)
			continue
		}
		o, err := d.original(c)
		if err != nil {
			return nil, err
		}
		at := slices.Index(cur, c.OrigName)
		moved := at != i
		if moved {
			cur = slices.Delete(cur, at, at+1)
			cur = slices.Insert(cur, min(i, len(cur)), c.OrigName)
		}
		if !moved && sameColumn(o, c) && o.Name == c.Name {
			continue
		}
		p := ""
		if moved {
			p = pos
		}
		if o.Name != c.Name {
			clauses = append(clauses, "CHANGE COLUMN "+myq(o.Name)+" "+g.column(c)+p)
		} else {
			clauses = append(clauses, "MODIFY COLUMN "+g.column(c)+p)
		}
	}
	return clauses, nil
}

func (g MySQL) addParts(d tableDiff) []string {
	var clauses []string
	if oldPK, newPK := d.pk(); !equalStrings(oldPK, newPK) && len(newPK) > 0 {
		clauses = append(clauses, "ADD PRIMARY KEY ("+quoteList(newPK, myq)+")")
	}
	_, ixs := d.indexes()
	for _, ix := range ixs {
		clauses = append(clauses, "ADD "+g.indexDef(ix))
	}
	_, fks := d.foreignKeys()
	for _, fk := range fks {
		clauses = append(clauses, "ADD "+g.fkDef(fk))
	}
	_, cks := d.checks(true)
	for _, ck := range cks {
		clauses = append(clauses, "ADD "+g.checkDef(ck))
	}
	return clauses
}

// tableOptionChanges lists the table options an edit changed.
func tableOptionChanges(old, nw Table) []string {
	oo, no := old.Options, nw.Options
	var out []string
	if no.Engine != oo.Engine && no.Engine != "" {
		out = append(out, "ENGINE="+no.Engine)
	}
	if no.Charset != oo.Charset && no.Charset != "" {
		out = append(out, "DEFAULT CHARSET="+no.Charset)
	}
	if no.Collation != oo.Collation && no.Collation != "" {
		out = append(out, "COLLATE="+no.Collation)
	}
	if no.AutoIncrement != oo.AutoIncrement && no.AutoIncrement != "" {
		out = append(out, "AUTO_INCREMENT="+no.AutoIncrement)
	}
	if no.RowFormat != oo.RowFormat && no.RowFormat != "" {
		out = append(out, "ROW_FORMAT="+no.RowFormat)
	}
	if nw.Comment != old.Comment {
		out = append(out, "COMMENT="+myStr(nw.Comment))
	}
	return out
}
