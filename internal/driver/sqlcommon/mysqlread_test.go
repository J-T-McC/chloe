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
		// With NO_BACKSLASH_ESCAPES the string ends at the backslash.
		"SELECT 'a\\' INTO OUTFILE '/tmp/x' -- '",
		"SELECT \"a\\\" INTO DUMPFILE '/tmp/x' -- \"",
		// /*! is refused even where it looks quoted: the server may not agree.
		"SELECT 'a\\' /*!CREATE TABLE x AS*/ SELECT 1 -- '",
		"SELECT 'unfinished",
		"SELECT 1 /* unfinished",
		"SELECT `x",
	}
	for _, s := range bad {
		if MySQLReadHazard(s) == nil {
			t.Errorf("accepted %q", s)
		}
	}
	good := []string{
		"SELECT \"INTO OUTFILE\", `into outfile` FROM t",
		"SELECT 'O\\'Brien', 'it''s' FROM t",
		"SELECT 'C:\\\\'",
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
