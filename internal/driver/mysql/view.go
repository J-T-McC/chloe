package mysql

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"chloe/internal/design"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
)

var algorithmRe = regexp.MustCompile(`ALGORITHM=(\w+)`)

func (c *Conn) LoadView(ctx context.Context, ref model.ObjectRef) (design.View, error) {
	rows, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT VIEW_DEFINITION, CHECK_OPTION, DEFINER, SECURITY_TYPE
		FROM information_schema.VIEWS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, ref.Database, ref.Name)
	if err != nil {
		return design.View{}, err
	}
	if len(rows) == 0 {
		return design.View{}, fmt.Errorf("view %s not found", ref.Name)
	}
	r := rows[0]
	v := design.View{Database: ref.Database, Name: ref.Name, OrigName: ref.Name, Security: r[3].String}
	// The server qualifies every name with the database; drop the current one.
	v.Definition = strings.ReplaceAll(r[0].String, "`"+strings.ReplaceAll(ref.Database, "`", "``")+"`.", "")
	if co := r[1].String; co != "NONE" {
		v.CheckOption = co
	}
	if user, host, ok := strings.Cut(r[2].String, "@"); ok {
		v.Definer = user + "@" + host
	}
	if sc, err := sqlcommon.QueryStrings(ctx, c.db, "SHOW CREATE VIEW "+dialect.QuoteIdent(ref.Database)+"."+dialect.QuoteIdent(ref.Name)); err == nil && len(sc) > 0 {
		if m := algorithmRe.FindStringSubmatch(sc[0][1].String); m != nil {
			v.Algorithm = m[1]
		}
	}
	return v, nil
}
