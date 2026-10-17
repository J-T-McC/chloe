package model

// ResultColumn describes one column of a result set.
type ResultColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // database type name
	Table    string `json:"table,omitempty"`
	Nullable bool   `json:"nullable"`
}

// StatementResult is the outcome of executing one statement.
// Rows are not included: they stay in Go memory and are paged via FetchRows.
type StatementResult struct {
	ResultID     string         `json:"resultId,omitempty"` // empty when the statement returned no rows
	SQL          string         `json:"sql"`
	Columns      []ResultColumn `json:"columns,omitempty"`
	RowCount     int            `json:"rowCount"`  // rows buffered so far
	Complete     bool           `json:"complete"`  // all rows fetched from server
	Truncated    bool           `json:"truncated"` // hit the row cap
	RowsAffected int64          `json:"rowsAffected"`
	DurationMs   int64          `json:"durationMs"`
	Error        string         `json:"error,omitempty"`
	ErrorLine    int            `json:"errorLine,omitempty"`
}

// RowPage is a window of rows. Cells are encoded as display strings; nil is SQL NULL.
// Binary values are hex-encoded with a "0x" prefix.
type RowPage struct {
	Offset   int         `json:"offset"`
	Rows     [][]*string `json:"rows"`
	Total    int         `json:"total"`
	Complete bool        `json:"complete"`
	// Cut lists the cells shortened to the requested length, as [row, column]
	// with row counted from Offset.
	Cut [][]int `json:"cut,omitempty"`
}
