package sqlcommon

import (
	"reflect"
	"testing"
)

func TestEnumValues(t *testing.T) {
	cases := map[string][]string{
		"enum('new','paid','shipped')": {"new", "paid", "shipped"},
		"set('a','it''s','b,c')":       {"a", "it's", "b,c"},
		"ENUM('x')":                    {"x"},
		"varchar(20)":                  nil,
	}
	for in, want := range cases {
		if got := EnumValues(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %q", in, got)
		}
	}
}
