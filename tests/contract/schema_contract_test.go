package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// schemaContractTest guards against schema drift: the Cassandra schema in
// cassandra/init/schema.cql must define every table and column that the Go
// services reference in their repository CQL statements. Historically the
// schema diverged from the repositories (missing tables such as
// ledger_transactions and reservations, mismatched column lists for accounts
// and payments), which only surfaced at runtime against a real Cassandra.
// This test fails at CI time instead.

var (
	repoTableRe  = regexp.MustCompile(`(?i)(?:INSERT INTO|UPDATE|DELETE FROM|FROM)\s+([a-z_][a-z0-9_]*)`)
	repoColumnRe = regexp.MustCompile(`(?i)(?:^|[\s,(])SET\s+([a-z_][a-z0-9_]*)\s*=|(?:^|[\s,(])([a-z_][a-z0-9_]*)\s*=\s*\?`)
	selectColsRe = regexp.MustCompile(`(?is)SELECT\s+(.+?)\s+FROM\s+([a-z_][a-z0-9_]*)`)
	insertColsRe = regexp.MustCompile(`(?is)INSERT INTO\s+([a-z_][a-z0-9_]*)\s*\(([^)]*)\)`)
	tableColsRe  = regexp.MustCompile(`(?is)CREATE TABLE IF NOT EXISTS\s+([a-z_][a-z0-9_]*)\s*\((.+?)\)\s*(?:WITH|;)`)
)

// extractColumnTokens pulls identifiers out of a comma/newline separated
// column fragment (also used for SELECT column lists).
func extractColumnTokens(fragment string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range regexp.MustCompile(`[a-z_][a-z0-9_]*`).FindAllString(fragment, -1) {
		upper := strings.ToUpper(tok)
		if upper == "SELECT" || upper == "FROM" || upper == "WHERE" || upper == "AND" ||
			upper == "OR" || upper == "LIMIT" || upper == "IF" || upper == "NOT" ||
			upper == "EXISTS" || upper == "ALLOW" || upper == "FILTERING" ||
			upper == "SET" || upper == "INTO" || upper == "VALUES" || upper == "UPDATE" ||
			upper == "DELETE" || upper == "BY" || upper == "ORDER" || upper == "ASC" ||
			upper == "DESC" || upper == "PRIMARY" || upper == "KEY" || upper == "USING" ||
			upper == "INSERT" || upper == "TTL" || upper == "SUM" || upper == "COUNT" ||
			upper == "AVG" || upper == "MIN" || upper == "MAX" || upper == "NOW" ||
			strings.HasPrefix(tok, "'") || strings.HasPrefix(tok, "\"") {
			continue
		}
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out
}

func extractRepoSQL(t *testing.T) map[string]map[string]bool {
	// table -> set(column)
	usage := map[string]map[string]bool{}
	add := func(table string, cols []string) {
		if usage[table] == nil {
			usage[table] = map[string]bool{}
		}
		for _, c := range cols {
			usage[table][c] = true
		}
	}

	repos, err := filepath.Glob(filepath.Join("..", "..", "services", "*", "internal", "repository", "*_repository.go"))
	if err != nil {
		t.Fatalf("globbing repositories: %v", err)
	}
	if len(repos) == 0 {
		t.Fatal("no repository files found - contract test is misconfigured")
	}

	for _, path := range repos {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		src := string(data)
		for _, m := range regexp.MustCompile("`([^`]*)`").FindAllStringSubmatch(src, -1) {
			stmt := m[1]
			stmt = strings.ReplaceAll(stmt, "\n", " ")

			for _, tm := range repoTableRe.FindAllStringSubmatch(stmt, -1) {
				table := tm[1]
				upper := strings.ToUpper(tm[0])
				// SELECT x FROM t -> columns come from selectColsRe separately
				if strings.HasPrefix(upper, "SELECT") {
					continue
				}
				add(table, nil)
			}
			for _, cm := range insertColsRe.FindAllStringSubmatch(stmt, -1) {
				add(cm[1], extractColumnTokens(cm[2]))
			}
			for _, sm := range selectColsRe.FindAllStringSubmatch(stmt, -1) {
				table := sm[2]
				// Column list is everything before FROM that is not a function
				// keyword (e.g. SUM(amount) -> amount)
				cols := extractColumnTokens(sm[1])
				add(table, cols)
			}
			// SET/WHERE columns on UPDATE/DELETE statements already caught via
			// the select path won't cover UPDATE; parse SET + WHERE here.
			for _, um := range repoColumnRe.FindAllStringSubmatch(stmt, -1) {
				table := ""
				for _, tm := range repoTableRe.FindAllStringSubmatch(stmt, -1) {
					if strings.HasPrefix(strings.ToUpper(tm[0]), "UPDATE") ||
						strings.HasPrefix(strings.ToUpper(tm[0]), "DELETE") ||
						strings.HasPrefix(strings.ToUpper(tm[0]), "INSERT") {
						table = tm[1]
					}
				}
				if table != "" {
					for _, g := range um[1:] {
						if g != "" {
							add(table, []string{g})
						}
					}
				}
			}
		}
	}
	return usage
}

func loadSchema(t *testing.T) (map[string]map[string]bool, map[string][]string) {
	// table -> set(column) ; plus ordered column list for primary key check
	cols := map[string]map[string]bool{}
	ordered := map[string][]string{}
	path := filepath.Join("..", "..", "cassandra", "init", "schema.cql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading schema.cql: %v", err)
	}
	src := strings.ReplaceAll(string(data), "\n", " ")
	for _, m := range tableColsRe.FindAllStringSubmatch(src, -1) {
		table := m[1]
		if cols[table] == nil {
			cols[table] = map[string]bool{}
		}
		// strip nested PRIMARY KEY (...) fragments and keep simple tokens
		fragment := m[2]
		// Remove frozen<...> collection types BEFORE tokenising: "frozen" is a
		// Cassandra TYPE keyword only when followed by '<' (e.g. frozen<map<...>>).
		// A plain column NAMED frozen (ledger_integrity_guards.frozen) is a real
		// column and must survive parsing — dropping it here caused a false
		// "missing columns: frozen" contract failure.
		fragment = regexp.MustCompile(`(?i)\bfrozen\s*<`).ReplaceAllString(fragment, "frozen_collection<")
		cols[table] = map[string]bool{}
		var list []string
		seen := map[string]bool{}
		for _, tok := range regexp.MustCompile(`[a-z_][a-z0-9_]*`).FindAllString(fragment, -1) {
			upper := strings.ToUpper(tok)
			if upper == "PRIMARY" || upper == "KEY" || upper == "WITH" || upper == "AND" ||
				upper == "CLUSTERING" || upper == "ORDER" || upper == "BY" || upper == "ASC" ||
				upper == "DESC" || upper == "IF" || upper == "NOT" || upper == "EXISTS" ||
				upper == "COMPACT" || upper == "STATIC" || upper == "TEXT" || upper == "BIGINT" ||
				upper == "UUID" || upper == "BOOLEAN" || upper == "INT" ||
				upper == "DOUBLE" || upper == "DATE" || upper == "LIST" || upper == "MAP" ||
				upper == "SET" || upper == "FROZEN_COLLECTION" || strings.Contains(tok, "<") {
				continue
			}
			if !seen[tok] {
				seen[tok] = true
				list = append(list, tok)
			}
		}
		ordered[table] = list
		for _, c := range list {
			cols[table][c] = true
		}
	}
	return cols, ordered
}

func TestSchemaCoversEveryRepositoryReference(t *testing.T) {
	usage := extractRepoSQL(t)
	schemaCols, _ := loadSchema(t)

	var problems []string
	for table, columns := range usage {
		sc, ok := schemaCols[table]
		if !ok {
			problems = append(problems, "missing table: "+table)
			continue
		}
		var cols []string
		for c := range columns {
			if !sc[c] {
				cols = append(cols, c)
			}
		}
		if len(cols) > 0 {
			sort.Strings(cols)
			problems = append(problems, table+" missing columns: "+strings.Join(cols, ", "))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("schema.cql is out of sync with repositories:\n\t%s", strings.Join(problems, "\n\t"))
	}
}
