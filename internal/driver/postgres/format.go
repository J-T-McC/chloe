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
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
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
