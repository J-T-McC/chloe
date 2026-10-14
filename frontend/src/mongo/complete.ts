// Autocomplete for MongoDB shell scripts: collections after "db.", methods after a
// collection or cursor, operators after "$" chosen by the call they're in, and the
// collection's field names as object keys and "$field" paths.

export interface MongoCatalog {
  collections: string[];
  databases: string[];
  /** Field paths of a collection when sampled (undefined while unknown). */
  fields(collection: string): string[] | undefined;
}

export interface MongoSuggestion {
  label: string;
  kind: "collection" | "method" | "operator" | "field" | "keyword" | "database" | "function";
  insertText: string;
  snippet?: boolean;
  detail?: string;
  /** Lower sorts first. */
  rank: number;
}

export interface MongoCompletion {
  suggestions: MongoSuggestion[];
  /** Offset where the text being replaced starts. */
  replaceStart: number;
  /** Collection the cursor's call works on, if any (lets the caller sample its fields). */
  collection?: string;
}

const DB_METHODS: [string, string, string][] = [
  ["getCollection", 'getCollection("$1")', "collection by name"],
  ["getCollectionNames", "getCollectionNames()", ""],
  ["getCollectionInfos", "getCollectionInfos($0)", ""],
  ["createCollection", 'createCollection("$1")', ""],
  ["createView", 'createView("$1", "$2", [$0])', "name, source, pipeline"],
  ["runCommand", "runCommand({ $0 })", ""],
  ["adminCommand", "adminCommand({ $0 })", ""],
  ["aggregate", "aggregate([$0])", "database aggregation"],
  ["getSiblingDB", 'getSiblingDB("$1")', ""],
  ["getName", "getName()", ""],
  ["stats", "stats()", ""],
  ["dropDatabase", "dropDatabase()", ""],
  ["serverStatus", "serverStatus()", ""],
  ["currentOp", "currentOp()", ""],
  ["killOp", "killOp($0)", ""],
  ["version", "version()", ""],
  ["hostInfo", "hostInfo()", ""],
  ["getUsers", "getUsers()", ""],
  ["createUser", "createUser({ user: \"$1\", pwd: \"$2\", roles: [$0] })", ""],
  ["dropUser", 'dropUser("$1")', ""],
];

const COLL_METHODS: [string, string, string][] = [
  ["find", "find({$0})", "query documents"],
  ["findOne", "findOne({$0})", ""],
  ["aggregate", "aggregate([\n\t{ $0 }\n])", "aggregation pipeline"],
  ["insertOne", "insertOne({$0})", ""],
  ["insertMany", "insertMany([{$0}])", ""],
  ["updateOne", "updateOne({$1}, { \\$set: {$0} })", "filter, update"],
  ["updateMany", "updateMany({$1}, { \\$set: {$0} })", "filter, update"],
  ["replaceOne", "replaceOne({$1}, {$0})", "filter, replacement"],
  ["deleteOne", "deleteOne({$0})", ""],
  ["deleteMany", "deleteMany({$0})", ""],
  ["countDocuments", "countDocuments({$0})", ""],
  ["estimatedDocumentCount", "estimatedDocumentCount()", ""],
  ["distinct", 'distinct("$1")', "field, filter"],
  ["findOneAndUpdate", "findOneAndUpdate({$1}, { \\$set: {$0} })", ""],
  ["findOneAndReplace", "findOneAndReplace({$1}, {$0})", ""],
  ["findOneAndDelete", "findOneAndDelete({$0})", ""],
  ["bulkWrite", "bulkWrite([$0])", ""],
  ["createIndex", "createIndex({ $0 })", "keys, options"],
  ["createIndexes", "createIndexes([$0])", ""],
  ["getIndexes", "getIndexes()", ""],
  ["dropIndex", 'dropIndex("$1")', ""],
  ["dropIndexes", "dropIndexes()", ""],
  ["stats", "stats()", ""],
  ["drop", "drop()", ""],
  ["renameCollection", 'renameCollection("$1")', ""],
  ["exists", "exists()", ""],
  ["count", "count({$0})", "deprecated: countDocuments"],
  ["insert", "insert({$0})", "deprecated: insertOne"],
  ["update", "update({$1}, {$0})", "deprecated: updateOne"],
  ["remove", "remove({$0})", "deprecated: deleteMany"],
];

const CURSOR_METHODS: [string, string, string][] = [
  ["sort", "sort({ $0 })", ""],
  ["limit", "limit($0)", ""],
  ["skip", "skip($0)", ""],
  ["projection", "projection({ $0 })", ""],
  ["toArray", "toArray()", ""],
  ["forEach", "forEach((doc) => {\n\t$0\n})", ""],
  ["map", "map((doc) => $0)", ""],
  ["count", "count()", ""],
  ["itcount", "itcount()", ""],
  ["next", "next()", ""],
  ["hasNext", "hasNext()", ""],
  ["explain", 'explain("executionStats")', ""],
  ["hint", "hint({ $0 })", ""],
  ["collation", 'collation({ locale: "$0" })', ""],
  ["batchSize", "batchSize($0)", ""],
  ["maxTimeMS", "maxTimeMS($0)", ""],
  ["comment", 'comment("$0")', ""],
  ["allowDiskUse", "allowDiskUse()", ""],
  ["pretty", "pretty()", ""],
  ["close", "close()", ""],
];

const QUERY_OPS = ["$eq", "$ne", "$gt", "$gte", "$lt", "$lte", "$in", "$nin", "$and", "$or", "$nor", "$not", "$exists", "$type", "$regex", "$options", "$elemMatch", "$size", "$all", "$expr", "$text", "$search", "$mod", "$where", "$geoWithin", "$near", "$jsonSchema"];
const UPDATE_OPS = ["$set", "$unset", "$inc", "$push", "$pull", "$addToSet", "$pop", "$rename", "$min", "$max", "$mul", "$currentDate", "$setOnInsert", "$each", "$position", "$slice", "$sort", "$pullAll"];
const STAGES = ["$match", "$group", "$project", "$sort", "$limit", "$skip", "$lookup", "$unwind", "$addFields", "$set", "$unset", "$count", "$facet", "$bucket", "$bucketAuto", "$replaceRoot", "$replaceWith", "$sample", "$sortByCount", "$unionWith", "$out", "$merge", "$graphLookup", "$densify", "$fill", "$setWindowFields", "$geoNear", "$redact", "$collStats", "$indexStats", "$documents"];
const EXPR_OPS = ["$sum", "$avg", "$min", "$max", "$first", "$last", "$push", "$addToSet", "$count", "$cond", "$ifNull", "$switch", "$eq", "$ne", "$gt", "$gte", "$lt", "$lte", "$and", "$or", "$not", "$in", "$add", "$subtract", "$multiply", "$divide", "$mod", "$round", "$abs", "$concat", "$substr", "$toUpper", "$toLower", "$split", "$trim", "$size", "$arrayElemAt", "$filter", "$map", "$reduce", "$concatArrays", "$slice", "$dateToString", "$year", "$month", "$dayOfMonth", "$hour", "$toString", "$toInt", "$toDouble", "$toDate", "$toObjectId", "$type", "$literal", "$mergeObjects", "$objectToArray", "$arrayToObject", "$let", "$dateTrunc", "$dateDiff", "$dateAdd"];
const GLOBALS: [string, string, string][] = [
  ["db", "db", "current database"],
  ["use", "use ", "switch database"],
  ["show dbs", "show dbs", ""],
  ["show collections", "show collections", ""],
  ["print", "print($0)", ""],
  ["printjson", "printjson($0)", ""],
  ["ObjectId", 'ObjectId("$0")', ""],
  ["ISODate", 'ISODate("$0")', ""],
  ["NumberLong", 'NumberLong("$0")', ""],
  ["NumberInt", "NumberInt($0)", ""],
  ["NumberDecimal", 'NumberDecimal("$0")', ""],
  ["UUID", 'UUID("$0")', ""],
  ["Timestamp", "Timestamp($1, $0)", ""],
  ["BinData", 'BinData(0, "$0")', ""],
  ["sleep", "sleep($0)", "milliseconds"],
];
const VALUE_GLOBALS = GLOBALS.filter(([l]) => /^[A-Z]/.test(l));

interface Tok {
  type: "ident" | "string" | "number" | "punct" | "regex";
  text: string;
  value: string;
  start: number;
  end: number;
}

/** A light JavaScript tokenizer: identifiers, strings, numbers and punctuation, comments skipped. */
export function tokenize(src: string): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  while (i < src.length) {
    const c = src[i];
    if (/\s/.test(c)) {
      i++;
    } else if (c === "/" && src[i + 1] === "/") {
      while (i < src.length && src[i] !== "\n") i++;
    } else if (c === "/" && src[i + 1] === "*") {
      const e = src.indexOf("*/", i + 2);
      i = e < 0 ? src.length : e + 2;
    } else if (c === '"' || c === "'" || c === "`") {
      const s = i++;
      while (i < src.length && src[i] !== c) {
        if (src[i] === "\\") i++;
        if (src[i] === "\n" && c !== "`") break;
        i++;
      }
      const closed = src[i] === c;
      if (closed) i++;
      out.push({ type: "string", text: src.slice(s, i), value: src.slice(s + 1, closed ? i - 1 : i), start: s, end: i });
    } else if (/[A-Za-z_$]/.test(c)) {
      const s = i;
      while (i < src.length && /[\w$]/.test(src[i])) i++;
      out.push({ type: "ident", text: src.slice(s, i), value: src.slice(s, i), start: s, end: i });
    } else if (/\d/.test(c)) {
      const s = i;
      while (i < src.length && /[\w.]/.test(src[i])) i++;
      out.push({ type: "number", text: src.slice(s, i), value: src.slice(s, i), start: s, end: i });
    } else {
      out.push({ type: "punct", text: c, value: c, start: i, end: i + 1 });
      i++;
    }
  }
  return out;
}

const isIdent = (s: string) => /^[A-Za-z_$][\w$]*$/.test(s);

interface Frame {
  open: string; // ( [ {
  at: number; // token index of the opener
  /** for "(": the method or function being called */
  callee?: string;
  /** for "(": the collection when the call is db.<coll>.<method>( */
  coll?: string;
  /** comma count at this level before the cursor */
  arg: number;
  /** for "{": whether the cursor is in key position */
}

/** The collection named by tokens ending at index i: db.coll, db["coll"], db.getCollection("coll"). */
function collectionBefore(toks: Tok[], i: number): string | undefined {
  // i is the index of the token before "."
  const t = toks[i];
  if (!t) return undefined;
  if (t.text === ")" && toks[i - 1]?.type === "string" && toks[i - 2]?.text === "(" && toks[i - 3]?.text === "getCollection" && toks[i - 4]?.text === "." && toks[i - 5]?.text === "db") {
    return toks[i - 1].value;
  }
  if (t.text === "]" && toks[i - 1]?.type === "string" && toks[i - 2]?.text === "[" && toks[i - 3]?.text === "db") return toks[i - 1].value;
  if (t.type === "ident" && toks[i - 1]?.text === "." && toks[i - 2]?.text === "db") return t.value;
  return undefined;
}

/** Start index of the call chain element whose ")" is at index i (skips back over the arguments). */
function matchOpen(toks: Tok[], close: number): number {
  const pair: Record<string, string> = { ")": "(", "]": "[", "}": "{" };
  let d = 0;
  for (let i = close; i >= 0; i--) {
    const t = toks[i].text;
    if (t === ")" || t === "]" || t === "}") d++;
    else if (t === pair[toks[close].text] || t === "(" || t === "[" || t === "{") {
      d--;
      if (d === 0) return i;
    }
  }
  return -1;
}

export function completeMongo(text: string, offset: number, cat: MongoCatalog): MongoCompletion {
  const toks = tokenize(text.slice(0, offset));
  const out: MongoSuggestion[] = [];
  let last = toks[toks.length - 1];
  // The word being typed (an identifier, or a $operator, touching the cursor).
  let prefix = "";
  let replaceStart = offset;
  if (last && last.end === offset && last.type === "ident") {
    prefix = last.value;
    replaceStart = last.start;
    toks.pop();
    last = toks[toks.length - 1];
  }
  // An unclosed string touching the cursor.
  const inString = last?.type === "string" && last.end === offset && (last.text.length === 1 || last.text[last.text.length - 1] !== last.text[0]);
  const push = (list: [string, string, string][], kind: MongoSuggestion["kind"], rank: number) => {
    for (const [label, insert, detail] of list) out.push({ label, kind, insertText: insert, snippet: insert.includes("$"), detail, rank });
  };

  // ---- inside a string: "$field" paths in aggregation expressions ------------------------
  if (inString) {
    const s = last!;
    const frames = framesOf(toks.slice(0, -1));
    const coll = lastWhere(frames, (f) => f.coll)?.coll;
    if (s.value.startsWith("$") && coll) {
      for (const f of cat.fields(coll) ?? []) out.push({ label: "$" + f, kind: "field", insertText: "$" + f, rank: 0 });
      return { suggestions: out, replaceStart: s.start + 1, collection: coll };
    }
    // getCollection("| or db["|
    const p = toks[toks.length - 2];
    if (p && (p.text === "(" && toks[toks.length - 3]?.text === "getCollection" || p.text === "[" && toks[toks.length - 3]?.text === "db")) {
      for (const c of cat.collections) out.push({ label: c, kind: "collection", insertText: c, rank: 0 });
      return { suggestions: out, replaceStart: s.start + 1 };
    }
    return { suggestions: [], replaceStart };
  }

  // ---- use <db> / show <what> ---------------------------------------------------------------
  if (last?.type === "ident" && last.value === "use" && atLineStart(text, last.start)) {
    for (const d of cat.databases) out.push({ label: d, kind: "database", insertText: d, rank: 0 });
    return { suggestions: out, replaceStart };
  }
  if (last?.type === "ident" && last.value === "show" && atLineStart(text, last.start)) {
    for (const w of ["dbs", "collections", "users", "roles"]) out.push({ label: w, kind: "keyword", insertText: w, rank: 0 });
    return { suggestions: out, replaceStart };
  }

  // ---- after "." ----------------------------------------------------------------------------
  if (last?.text === ".") {
    const before = toks[toks.length - 2];
    if (before?.text === "db" && toks[toks.length - 3]?.text !== ".") {
      for (const c of cat.collections) {
        out.push(isIdent(c) ? { label: c, kind: "collection", insertText: c, rank: 0 } : { label: c, kind: "collection", insertText: `getCollection("${c}")`, rank: 0 });
      }
      push(DB_METHODS, "method", 1);
      return { suggestions: out, replaceStart };
    }
    const coll = collectionBefore(toks, toks.length - 2);
    if (coll !== undefined) {
      push(COLL_METHODS, "method", 0);
      return { suggestions: out, replaceStart, collection: coll };
    }
    if (before?.text === ")") {
      // a call chain: find(...).| or aggregate(...).|
      const open = matchOpen(toks, toks.length - 2);
      const callee = toks[open - 1]?.value;
      if (callee && ["find", "aggregate", "sort", "limit", "skip", "projection", "hint", "collation", "batchSize", "comment", "maxTimeMS", "allowDiskUse", "pretty", "map"].includes(callee)) {
        push(CURSOR_METHODS, "method", 0);
        return { suggestions: out, replaceStart };
      }
    }
    return { suggestions: [], replaceStart };
  }

  // ---- inside calls and object literals -------------------------------------------------------
  const frames = framesOf(toks);
  const top = frames[frames.length - 1];
  const call = lastWhere(frames, (f) => f.open === "(");
  const coll = call?.coll ?? lastWhere(frames, (f) => f.coll)?.coll;
  const operatorPrefix = prefix.startsWith("$");

  if (top?.open === "{") {
    const keyPos = last && (last.text === "{" || last.text === ",");
    const ops = operatorsFor(frames);
    if (keyPos || operatorPrefix) {
      for (const op of ops.list) out.push({ label: op, kind: "operator", insertText: op, detail: ops.what, rank: operatorPrefix ? 0 : 2 });
      if (!operatorPrefix && ops.fields && coll) {
        for (const f of cat.fields(coll) ?? []) out.push({ label: f, kind: "field", insertText: isIdent(f) ? f : JSON.stringify(f), rank: 0 });
      }
      return { suggestions: out, replaceStart, collection: coll };
    }
  }
  // values: BSON constructors, then globals
  push(VALUE_GLOBALS, "function", 1);
  if (!top) push(GLOBALS.filter(([l]) => !/^[A-Z]/.test(l)), "keyword", 0);
  return { suggestions: out, replaceStart, collection: coll };
}

function lastWhere<T>(list: T[], pred: (x: T) => unknown): T | undefined {
  for (let i = list.length - 1; i >= 0; i--) if (pred(list[i])) return list[i];
  return undefined;
}

function atLineStart(text: string, at: number) {
  return /(^|\n)[ \t]*$/.test(text.slice(0, at));
}

/** Open brackets around the end of toks, innermost last, with call names and argument numbers. */
function framesOf(toks: Tok[]): Frame[] {
  const stack: Frame[] = [];
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i].text;
    if (toks[i].type !== "punct") continue;
    if (t === "(" || t === "[" || t === "{") {
      const f: Frame = { open: t, at: i, arg: 0 };
      if (t === "(") {
        f.callee = toks[i - 1]?.type === "ident" ? toks[i - 1].value : undefined;
        if (toks[i - 2]?.text === ".") f.coll = collectionBefore(toks, i - 3);
      }
      stack.push(f);
    } else if (t === ")" || t === "]" || t === "}") {
      stack.pop();
    } else if (t === "," && stack.length) {
      stack[stack.length - 1].arg++;
    }
  }
  return stack;
}

const FILTER_METHODS = new Set(["find", "findOne", "countDocuments", "count", "deleteOne", "deleteMany", "remove", "distinct", "findOneAndDelete"]);
const UPDATE_METHODS = new Set(["updateOne", "updateMany", "update", "findOneAndUpdate"]);

/** Which operators fit the object the cursor is in, and whether field names do. */
function operatorsFor(frames: Frame[]): { list: string[]; what: string; fields: boolean } {
  const call = lastWhere(frames, (f) => f.open === "(");
  if (!call) return { list: QUERY_OPS, what: "query operator", fields: true };
  const inner = frames.slice(frames.indexOf(call) + 1);
  const m = call.callee ?? "";
  if (m === "aggregate" || m === "createView") {
    // [ { $stage: { ... } } ]: a stage name directly in a pipeline element, expressions below
    const arr = inner.findIndex((f) => f.open === "[");
    const depthInStage = inner.length - arr - 1;
    if (arr >= 0 && depthInStage === 1) return { list: STAGES, what: "stage", fields: false };
    // $match takes a query; $project/$group/$addFields take field names and expressions
    return { list: [...EXPR_OPS, ...QUERY_OPS.filter((o) => !EXPR_OPS.includes(o))], what: "expression", fields: true };
  }
  if (UPDATE_METHODS.has(m) && call.arg === 1) {
    return inner.length <= 1 ? { list: UPDATE_OPS, what: "update operator", fields: false } : { list: QUERY_OPS, what: "operator", fields: true };
  }
  if (FILTER_METHODS.has(m) || UPDATE_METHODS.has(m) || m === "replaceOne" || m === "findOneAndReplace") {
    return { list: QUERY_OPS, what: "query operator", fields: true };
  }
  return { list: QUERY_OPS, what: "operator", fields: true };
}
