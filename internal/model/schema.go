package model

// ObjectKind is a node type in the object tree.
type ObjectKind string

const (
	KindDatabase  ObjectKind = "database"
	KindSchema    ObjectKind = "schema"
	KindTable     ObjectKind = "table"
	KindView      ObjectKind = "view"
	KindMatView   ObjectKind = "materialized_view"
	KindFunction  ObjectKind = "function"
	KindProcedure ObjectKind = "procedure"
	KindTrigger   ObjectKind = "trigger"
	KindEvent     ObjectKind = "event"
	KindSequence  ObjectKind = "sequence"
	KindType      ObjectKind = "type"
	KindIndex     ObjectKind = "index" // SQLite lists indexes in the tree
)

// ObjectRef locates an object. Database is the MySQL database or PG database;
// Schema is the PG schema (empty for MySQL).
type ObjectRef struct {
	Database string     `json:"database"`
	Schema   string     `json:"schema,omitempty"`
	Name     string     `json:"name"`
	Kind     ObjectKind `json:"kind"`
	// Signature distinguishes overloaded functions (PostgreSQL identity arguments).
	Signature string `json:"signature,omitempty"`
}

// ObjectInfo is a row in an object list (tree children / object list view).
type ObjectInfo struct {
	ObjectRef
	Comment    string `json:"comment,omitempty"`
	Rows       int64  `json:"rows,omitempty"` // estimated
	DataSize   int64  `json:"dataSize,omitempty"`
	Engine     string `json:"engine,omitempty"`
	Created    string `json:"created,omitempty"`
	Modified   string `json:"modified,omitempty"`
	Owner      string `json:"owner,omitempty"`
	ReturnType string `json:"returnType,omitempty"`
}

type Column struct {
	Name          string  `json:"name"`
	Type          string  `json:"type"`     // full type as declared, e.g. varchar(255)
	DataType      string  `json:"dataType"` // base type, e.g. varchar
	Nullable      bool    `json:"nullable"`
	Default       *string `json:"default,omitempty"`
	PrimaryKey    bool    `json:"primaryKey"`
	AutoIncrement bool    `json:"autoIncrement"`
	Comment       string  `json:"comment,omitempty"`
	Position      int     `json:"position"`
}

type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	Method  string   `json:"method,omitempty"`
}

type ForeignKey struct {
	Name        string   `json:"name"`
	Columns     []string `json:"columns"`
	RefSchema   string   `json:"refSchema,omitempty"`
	RefDatabase string   `json:"refDatabase,omitempty"`
	RefTable    string   `json:"refTable"`
	RefColumns  []string `json:"refColumns"`
	OnUpdate    string   `json:"onUpdate,omitempty"`
	OnDelete    string   `json:"onDelete,omitempty"`
}

// TableDetail is everything the designer, grid and autocomplete need about one table.
type TableDetail struct {
	ObjectRef
	Columns     []Column     `json:"columns"`
	Indexes     []Index      `json:"indexes"`
	ForeignKeys []ForeignKey `json:"foreignKeys"`
}

// CatalogColumn is a flattened column row used to seed autocomplete for a whole schema at once.
type CatalogColumn struct {
	Schema   string `json:"schema,omitempty"`
	Table    string `json:"table"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	PK       bool   `json:"pk,omitempty"`
	Nullable bool   `json:"nullable,omitempty"`
	// EnumValues are the allowed values of enum (and MySQL set) columns.
	EnumValues []string `json:"enumValues,omitempty"`
}

// CatalogFK is a flattened foreign key used for JOIN suggestions.
type CatalogFK struct {
	Schema     string   `json:"schema,omitempty"`
	Table      string   `json:"table"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"refSchema,omitempty"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
}

// Catalog is a bulk snapshot of one database (MySQL) or one database's schemas (PG),
// loaded in a few queries and cached on the frontend for autocomplete.
type Catalog struct {
	Database string          `json:"database"`
	Schemas  []string        `json:"schemas,omitempty"`
	Objects  []ObjectInfo    `json:"objects"`
	Columns  []CatalogColumn `json:"columns"`
	FKs      []CatalogFK     `json:"fks"`
}

// Process is one server session, as shown by the server monitor.
type Process struct {
	ID          string `json:"id"`
	User        string `json:"user"`
	Host        string `json:"host"`
	Database    string `json:"database"`
	Application string `json:"application,omitempty"`
	Command     string `json:"command,omitempty"` // MySQL: Query/Sleep/...; PG: backend type
	State       string `json:"state"`
	TimeSec     int64  `json:"timeSec"` // seconds in the current state/query
	Query       string `json:"query"`
	// Self marks sessions opened by this Chloe connection.
	Self bool `json:"self"`
}

// ServerVar is a server variable or status counter.
type ServerVar struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// ServerInfo summarises an open connection.
type ServerInfo struct {
	Version  string `json:"version"`
	Sessions int    `json:"sessions"` // server connections this profile holds open
	Host     string `json:"host"`
	User     string `json:"user"`
	Via      string `json:"via"` // "direct", "SSH host:port", "socket path"
	SSL      bool   `json:"ssl"`
}
