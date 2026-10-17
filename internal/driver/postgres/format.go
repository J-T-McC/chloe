package postgres

import (
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"strings"

	"chloe/internal/driver/sqlcommon"
)

func formatValue(v any, typeName string) string {
	switch x := v.(type) {
	case []byte:
		if typeName == "BYTEA" {
			return `\x` + hex.EncodeToString(x)
		}
	case [16]byte: // uuid
		var b [36]byte
		hex.Encode(b[0:8], x[0:4])
		hex.Encode(b[9:13], x[4:6])
		hex.Encode(b[14:18], x[6:8])
		hex.Encode(b[19:23], x[8:10])
		hex.Encode(b[24:], x[10:])
		b[8], b[13], b[18], b[23] = '-', '-', '-', '-'
		return string(b[:])
	case driver.Valuer:
		if inner, err := x.Value(); err == nil && inner != nil {
			return sqlcommon.FormatValue(inner, typeName)
		}
	}
	if strings.HasPrefix(typeName, "_") { // arrays arrive in text form already
		return fmt.Sprint(v)
	}
	return sqlcommon.FormatValue(v, typeName)
}
