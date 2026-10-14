package postgres

var builtinFunctions = []string{
	// aggregate
	"array_agg", "avg", "bit_and", "bit_or", "bool_and", "bool_or", "count", "every", "json_agg", "jsonb_agg",
	"json_object_agg", "jsonb_object_agg", "max", "min", "string_agg", "sum", "stddev", "stddev_pop", "stddev_samp",
	"variance", "var_pop", "var_samp", "percentile_cont", "percentile_disc", "mode", "corr", "covar_pop", "covar_samp",
	// window
	"row_number", "rank", "dense_rank", "percent_rank", "cume_dist", "ntile", "lag", "lead", "first_value", "last_value", "nth_value",
	// conditional
	"coalesce", "nullif", "greatest", "least",
	// string
	"ascii", "bit_length", "btrim", "char_length", "character_length", "chr", "concat", "concat_ws", "format", "initcap",
	"left", "length", "lower", "lpad", "ltrim", "md5", "octet_length", "overlay", "position", "quote_ident", "quote_literal",
	"quote_nullable", "regexp_count", "regexp_instr", "regexp_like", "regexp_match", "regexp_matches", "regexp_replace",
	"regexp_split_to_array", "regexp_split_to_table", "regexp_substr", "repeat", "replace", "reverse", "right", "rpad", "rtrim",
	"split_part", "starts_with", "string_to_array", "string_to_table", "strpos", "substr", "substring", "to_hex", "translate",
	"trim", "unistr", "upper",
	// math
	"abs", "cbrt", "ceil", "ceiling", "degrees", "div", "exp", "factorial", "floor", "gcd", "lcm", "ln", "log", "log10", "mod",
	"pi", "power", "radians", "random", "round", "scale", "sign", "sqrt", "trunc", "width_bucket", "sin", "cos", "tan", "atan2",
	// date/time
	"age", "clock_timestamp", "current_date", "current_time", "current_timestamp", "date_bin", "date_part", "date_trunc",
	"extract", "isfinite", "justify_days", "justify_hours", "justify_interval", "localtime", "localtimestamp", "make_date",
	"make_interval", "make_time", "make_timestamp", "make_timestamptz", "now", "statement_timestamp", "timeofday",
	"transaction_timestamp", "to_timestamp", "to_char", "to_date", "to_number",
	// json
	"json_build_array", "json_build_object", "json_each", "json_each_text", "json_extract_path", "json_extract_path_text",
	"json_array_elements", "json_array_elements_text", "json_array_length", "json_object_keys", "json_populate_record",
	"json_typeof", "jsonb_build_array", "jsonb_build_object", "jsonb_each", "jsonb_each_text", "jsonb_extract_path",
	"jsonb_extract_path_text", "jsonb_array_elements", "jsonb_array_elements_text", "jsonb_array_length", "jsonb_insert",
	"jsonb_object_keys", "jsonb_path_exists", "jsonb_path_query", "jsonb_path_query_array", "jsonb_path_query_first",
	"jsonb_pretty", "jsonb_set", "jsonb_set_lax", "jsonb_strip_nulls", "jsonb_typeof", "to_json", "to_jsonb", "row_to_json",
	// arrays & sets
	"array_append", "array_cat", "array_dims", "array_length", "array_lower", "array_position", "array_positions",
	"array_prepend", "array_remove", "array_replace", "array_to_string", "array_upper", "cardinality", "unnest",
	"generate_series", "generate_subscripts",
	// system / misc
	"current_database", "current_schema", "current_schemas", "current_user", "session_user", "version", "pg_backend_pid",
	"pg_cancel_backend", "pg_terminate_backend", "pg_size_pretty", "pg_total_relation_size", "pg_relation_size",
	"pg_database_size", "pg_table_size", "pg_indexes_size", "pg_sleep", "gen_random_uuid", "nextval", "currval", "setval",
	"lastval", "has_table_privilege", "pg_typeof", "col_description", "obj_description", "format_type", "encode", "decode",
	"cast", "exists",
}
