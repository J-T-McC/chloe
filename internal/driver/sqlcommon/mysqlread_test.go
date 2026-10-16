package sqlcommon

import "testing"

func TestMySQLReadHazard(t *testing.T) {
	bad := []string{
		"/*!CREATE TABLE pwned AS*/ SELECT * FROM src",
		"SELECT 1 /*!50000 , (SELECT 2) */",
		"/*M!CREATE TABLE x AS*/ SELECT 1",
		"SELECT * FROM t INTO OUTFILE '/tmp/x'",
		"SELECT * FROM t INTO/**/OUTFILE '/tmp/x'",
		"SELECT * FROM t INTO -- x\nOUTFILE '/tmp/x'",
		"SELECT * FROM t INTO # x\nDUMPFILE '/tmp/x'",
		"SELECT 'a\\'' INTO OUTFILE '/tmp/x'",
	}
	for _, s := range bad {
		if MySQLReadHazard(s) == nil {
			t.Errorf("accepted %q", s)
		}
	}
	good := []string{
		"SELECT '/*!x*/', \"INTO OUTFILE\", `into outfile` FROM t",
		"SELECT 1 /* plain */ -- INTO OUTFILE\n",
		"SELECT /*+ MAX_EXECUTION_TIME(1000) */ * FROM t",
		"SELECT 'it''s' INTO @v",
		"SELECT 5--1",
	}
	for _, s := range good {
		if err := MySQLReadHazard(s); err != nil {
			t.Errorf("rejected %q: %v", s, err)
		}
	}
}
