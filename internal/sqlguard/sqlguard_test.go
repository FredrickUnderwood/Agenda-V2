package sqlguard

import (
	"errors"
	"strings"
	"testing"
)

func TestEnsureReadOnlyAccepts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain select", "SELECT 1", "SELECT 1"},
		{"lowercase", "select id from t", "select id from t"},
		{"trailing semicolon", "SELECT 1;", "SELECT 1"},
		{"trailing semicolon and space", "SELECT 1 ;  \n", "SELECT 1"},
		{"leading whitespace", "\n\t SELECT 1", "SELECT 1"},
		{"cte", "WITH x AS (SELECT 1) SELECT * FROM x", "WITH x AS (SELECT 1) SELECT * FROM x"},
		{"parenthesized union", "(SELECT 1) UNION (SELECT 2)", "(SELECT 1) UNION (SELECT 2)"},
		{"show", "SHOW TABLES", "SHOW TABLES"},
		{"describe", "DESCRIBE user", "DESCRIBE user"},
		{"desc", "DESC user", "DESC user"},
		{"explain", "EXPLAIN SELECT 1", "EXPLAIN SELECT 1"},
		{"line comment stripped", "-- a note\nSELECT 1", "SELECT 1"},
		{"hash comment stripped", "# a note\nSELECT 1", "SELECT 1"},
		{"block comment stripped", "/* note */SELECT 1", "SELECT 1"},
		{"optimizer hint kept as comment", "SELECT /*+ NO_ICP(t) */ 1", "SELECT   1"},
		// A semicolon or a banned keyword inside a literal is data, not syntax.
		{"semicolon in literal", "SELECT ';'", "SELECT ';'"},
		{"outfile word in literal", "SELECT 'into outfile'", "SELECT 'into outfile'"},
		{"drop word in literal", "SELECT * FROM t WHERE name = 'drop table x'", "SELECT * FROM t WHERE name = 'drop table x'"},
		{"escaped quote in literal", `SELECT 'it''s ok'`, `SELECT 'it''s ok'`},
		{"backslash escaped quote", `SELECT 'a\'; DROP'`, `SELECT 'a\'; DROP'`},
		{"backtick identifier", "SELECT `select` FROM `t`", "SELECT `select` FROM `t`"},
		{"double dash without space is minus", "SELECT 1--2", "SELECT 1--2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EnsureReadOnly(tc.in)
			if err != nil {
				t.Fatalf("EnsureReadOnly(%q) = error %v, want accepted", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("EnsureReadOnly(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEnsureReadOnlyRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   \n\t "},
		{"semicolon only", ";"},
		{"insert", "INSERT INTO t VALUES (1)"},
		{"update", "UPDATE t SET a = 1"},
		{"delete", "DELETE FROM t"},
		{"replace", "REPLACE INTO t VALUES (1)"},
		{"drop", "DROP TABLE t"},
		{"create", "CREATE TABLE t (id int)"},
		{"alter", "ALTER TABLE t ADD COLUMN c int"},
		{"truncate", "TRUNCATE TABLE t"},
		{"grant", "GRANT ALL ON *.* TO 'x'@'%'"},
		{"set", "SET GLOBAL max_connections = 1"},
		{"call procedure", "CALL do_something()"},
		{"load data", "LOAD DATA INFILE '/etc/passwd' INTO TABLE t"},
		{"analyze writes stats", "ANALYZE TABLE t"},
		{"multi statement", "SELECT 1; DROP TABLE t"},
		{"multi statement trailing", "SELECT 1; DROP TABLE t;"},
		{"write hidden after comment", "SELECT 1; -- x\nDROP TABLE t"},
		{"executable comment", "/*!INSERT INTO t VALUES (1)*/"},
		{"executable comment after select", "SELECT 1 /*!, (SELECT 1) */"},
		{"versioned executable comment", "/*!40101 SET NAMES utf8 */"},
		{"select into outfile", "SELECT * FROM t INTO OUTFILE '/tmp/x'"},
		{"select into dumpfile", "SELECT a FROM t INTO DUMPFILE '/tmp/x'"},
		{"outfile mixed case", "SELECT * FROM t InTo   OutFile '/tmp/x'"},
		{"outfile after newline", "SELECT *\nFROM t\nINTO\nOUTFILE '/tmp/x'"},
		{"unterminated literal", "SELECT 'abc"},
		{"unterminated block comment", "SELECT 1 /* abc"},
		{"write hidden by comment prefix", "/* SELECT */ DELETE FROM t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EnsureReadOnly(tc.in)
			if err == nil {
				t.Fatalf("EnsureReadOnly(%q) = %q, want rejected", tc.in, got)
			}
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("EnsureReadOnly(%q) error %v does not wrap ErrRejected", tc.in, err)
			}
		})
	}
}

// A rejection message is shown to the operator, so it must name the statement
// kind rather than just saying "no".
func TestRejectionMessageNamesTheVerb(t *testing.T) {
	_, err := EnsureReadOnly("DELETE FROM t")
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), "DELETE") {
		t.Fatalf("error %q should name the rejected verb", err)
	}
}
