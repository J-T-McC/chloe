package sqlite

// builtinFunctions are SQLite's core, date, math, aggregate, window and JSON functions.
var builtinFunctions = []string{
	"abs", "changes", "char", "coalesce", "concat", "concat_ws", "format", "glob", "hex", "ifnull", "iif", "instr",
	"last_insert_rowid", "length", "like", "likelihood", "likely", "lower", "ltrim", "max", "min", "nullif", "octet_length",
	"printf", "quote", "random", "randomblob", "replace", "round", "rtrim", "sign", "soundex", "sqlite_version", "substr",
	"substring", "total_changes", "trim", "typeof", "unhex", "unicode", "unlikely", "upper", "zeroblob",
	"date", "time", "datetime", "julianday", "unixepoch", "strftime", "timediff",
	"acos", "asin", "atan", "atan2", "ceil", "cos", "degrees", "exp", "floor", "ln", "log", "log10", "log2", "mod", "pi",
	"pow", "power", "radians", "sin", "sqrt", "tan", "trunc",
	"avg", "count", "group_concat", "string_agg", "sum", "total",
	"row_number", "rank", "dense_rank", "percent_rank", "cume_dist", "ntile", "lag", "lead", "first_value", "last_value", "nth_value",
	"json", "jsonb", "json_array", "json_array_length", "json_error_position", "json_extract", "jsonb_extract", "json_insert",
	"json_object", "json_patch", "json_pretty", "json_remove", "json_replace", "json_set", "json_type", "json_valid", "json_quote",
	"json_group_array", "json_group_object", "json_each", "json_tree",
}
