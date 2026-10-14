package design

import (
	"slices"
	"strings"
)

// findBy returns the first element whose key equals name.
func findBy[T any](list []T, name string, key func(T) string) (T, bool) {
	var zero T
	if name == "" {
		return zero, false
	}
	for _, x := range list {
		if key(x) == name {
			return x, true
		}
	}
	return zero, false
}

func byOrig[T any](list []T, key func(T) string) map[string]T {
	m := make(map[string]T, len(list))
	for _, x := range list {
		m[key(x)] = x
	}
	return m
}

func equalStrings(a, b []string) bool { return slices.Equal(a, b) }

func pkCols(pk *PrimaryKey) []string {
	if pk == nil {
		return nil
	}
	return pk.Columns
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func identityEqual(a, b *IdentityOpts) bool {
	if a == nil {
		a = &IdentityOpts{}
	}
	if b == nil {
		b = &IdentityOpts{}
	}
	return *a == *b
}

// sameColumn compares everything but the name.
func sameColumn(a, b Column) bool {
	return strings.EqualFold(a.Type, b.Type) && a.Length == b.Length && a.Scale == b.Scale && a.Nullable == b.Nullable &&
		strPtrEqual(a.Default, b.Default) && a.Comment == b.Comment && a.AutoIncrement == b.AutoIncrement &&
		a.Unsigned == b.Unsigned && a.Zerofill == b.Zerofill && a.OnUpdateNow == b.OnUpdateNow &&
		a.Charset == b.Charset && a.Collation == b.Collation && a.GeneratedExpr == b.GeneratedExpr &&
		a.GeneratedStored == b.GeneratedStored && a.Identity == b.Identity && identityEqual(a.IdentityOpts, b.IdentityOpts)
}

func sameIndex(a, b Index) bool {
	return a.Name == b.Name && slices.Equal(a.Columns, b.Columns) && a.Kind == b.Kind &&
		strings.EqualFold(a.Method, b.Method) && a.Where == b.Where && a.Comment == b.Comment && a.Invisible == b.Invisible
}

func sameFK(a, b ForeignKey) bool {
	norm := func(s string) string {
		if s == "" {
			return "NO ACTION"
		}
		return strings.ToUpper(s)
	}
	return a.Name == b.Name && slices.Equal(a.Columns, b.Columns) && a.RefDatabase == b.RefDatabase && a.RefSchema == b.RefSchema &&
		a.RefTable == b.RefTable && slices.Equal(a.RefColumns, b.RefColumns) && norm(a.OnDelete) == norm(b.OnDelete) && norm(a.OnUpdate) == norm(b.OnUpdate)
}

func sameTrigger(a, b Trigger) bool {
	return a.Name == b.Name && a.Timing == b.Timing && slices.Equal(a.Events, b.Events) && strings.TrimSpace(a.Body) == strings.TrimSpace(b.Body) &&
		a.ForEachRow == b.ForEachRow && a.Function == b.Function && a.When == b.When
}
